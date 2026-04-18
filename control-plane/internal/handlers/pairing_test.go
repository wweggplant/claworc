package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
)

func buildJSONRequest(t *testing.T, method, url string, body []byte, user *database.User, chiParams map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	for k, v := range chiParams {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if user != nil {
		ctx = middleware.WithUser(ctx, user)
	}
	return req.WithContext(ctx)
}

func pairingSSHServer(t *testing.T, responder func(cmd string) (stdout, stderr string, exitCode uint32)) (string, ssh.Signer, func()) {
	t.Helper()

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	authorizedSigner, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	authorizedPub, _, _, _, err := ssh.ParseAuthorizedKey(pubKeyBytes)
	if err != nil {
		t.Fatalf("parse authorized public key: %v", err)
	}

	_, hostKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		t.Fatalf("parse host key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(authorizedPub) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				sshConn, chans, reqs, err := ssh.NewServerConn(netConn, cfg)
				if err != nil {
					netConn.Close()
					return
				}
				defer sshConn.Close()

				go func() {
					for req := range reqs {
						if req.WantReply {
							req.Reply(true, nil)
						}
					}
				}()

				for newChan := range chans {
					if newChan.ChannelType() != "session" {
						newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
						continue
					}
					ch, requests, err := newChan.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer ch.Close()
						for req := range requests {
							if req.Type != "exec" {
								if req.WantReply {
									req.Reply(true, nil)
								}
								continue
							}
							var payload struct{ Value string }
							if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
								if req.WantReply {
									req.Reply(false, nil)
								}
								return
							}
							stdout, stderr, exitCode := responder(payload.Value)
							if req.WantReply {
								req.Reply(true, nil)
							}
							if stdout != "" {
								_, _ = ch.Write([]byte(stdout))
							}
							if stderr != "" {
								_, _ = ch.Stderr().Write([]byte(stderr))
							}
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: exitCode}))
							return
						}
					}()
				}
			}()
		}
	}()

	cleanup := func() {
		listener.Close()
		<-done
	}

	return listener.Addr().String(), authorizedSigner, cleanup
}

func setupPairingEnv(t *testing.T, responder func(cmd string) (stdout, stderr string, exitCode uint32)) (database.Instance, *database.User) {
	t.Helper()
	setupTestDB(t)

	addr, signer, cleanup := pairingSSHServer(t, responder)
	t.Cleanup(cleanup)

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	SSHMgr = sshproxy.NewSSHManager(signer, "test-pubkey")
	t.Cleanup(func() {
		SSHMgr.CloseAll()
		SSHMgr = nil
	})

	orchestrator.Set(&mockOrchestrator{sshHost: host, sshPort: port})
	t.Cleanup(func() { orchestrator.Set(nil) })

	inst := createTestInstance(t, "bot-pairing", "Pairing Test")
	user := createTestUser(t, "admin")
	return inst, user
}

func TestListFeishuPairingRequests_Success(t *testing.T) {
	inst, user := setupPairingEnv(t, func(cmd string) (string, string, uint32) {
		if !strings.Contains(cmd, "pairing") || !strings.Contains(cmd, "list") || !strings.Contains(cmd, "feishu") {
			return "", "unexpected command", 1
		}
		return `{"channel":"feishu","requests":[{"code":"ABCDEFGH","user":"alice"}]}`, "", 0
	})

	req := buildRequest(t, "GET", "/api/v1/instances/1/pairing/feishu", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	ListFeishuPairingRequests(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var body struct {
		Pending []map[string]any `json:"pending"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Pending) != 1 {
		t.Fatalf("expected 1 pending item, got %d", len(body.Pending))
	}
	if got := body.Pending[0]["code"]; got != "ABCDEFGH" {
		t.Fatalf("expected code ABCDEFGH, got %v", got)
	}
}

func TestListFeishuPairingRequests_PluginLogPrefix(t *testing.T) {
	inst, user := setupPairingEnv(t, func(cmd string) (string, string, uint32) {
		if !strings.Contains(cmd, "pairing") || !strings.Contains(cmd, "list") {
			return "", "unexpected command", 1
		}
		// Simulate real OpenClaw output with plugin log lines on stdout
		return "[plugins] feishu_doc: Registered feishu_doc\n[plugins] feishu_chat: Registered feishu_chat tool\n{\"channel\":\"feishu\",\"requests\":[{\"code\":\"TESTCODE1\",\"id\":\"user123\"}]}", "", 0
	})

	req := buildRequest(t, "GET", "/api/v1/instances/1/pairing/feishu", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	ListFeishuPairingRequests(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var body struct {
		Pending []map[string]any `json:"pending"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Pending) != 1 {
		t.Fatalf("expected 1 pending item, got %d", len(body.Pending))
	}
	if got := body.Pending[0]["code"]; got != "TESTCODE1" {
		t.Fatalf("expected code TESTCODE1, got %v", got)
	}
}

func TestExtractLastJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"pure JSON", `{"a":1}`, `{"a":1}`},
		{"prefix + JSON", "log line\n{\"a\":1}", `{"a":1}`},
		{"plugin logs", "[plugins] foo: bar\n{\"channel\":\"feishu\",\"requests\":[]}", `{"channel":"feishu","requests":[]}`},
		{"no JSON", "just text", ""},
		{"empty", "", ""},
		{"nested braces", "prefix\n{\"a\":{\"b\":2},\"c\":3}", `{"a":{"b":2},"c":3}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLastJSON(tt.in)
			if got != tt.want {
				t.Errorf("extractLastJSON(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestApproveFeishuPairingRequest_InvalidCode(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-pairing-invalid", "Pairing Invalid")
	user := createTestUser(t, "admin")

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	SSHMgr = sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	t.Cleanup(func() {
		SSHMgr.CloseAll()
		SSHMgr = nil
	})
	orchestrator.Set(&mockOrchestrator{})
	t.Cleanup(func() { orchestrator.Set(nil) })

	req := buildJSONRequest(t, "POST", "/api/v1/instances/1/pairing/feishu/approve", []byte(`{"code":"BADCODE0"}`), user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	ApproveFeishuPairingRequest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Invalid pairing code format") {
		t.Fatalf("expected invalid code error, got %s", w.Body.String())
	}
}

func TestApproveFeishuPairingRequest_Success(t *testing.T) {
	inst, user := setupPairingEnv(t, func(cmd string) (string, string, uint32) {
		if !strings.Contains(cmd, "pairing") || !strings.Contains(cmd, "approve") || !strings.Contains(cmd, "ABCDEFGH") {
			return "", "unexpected command", 1
		}
		return "approved", "", 0
	})

	req := buildJSONRequest(t, "POST", "/api/v1/instances/1/pairing/feishu/approve", []byte(`{"code":"ABCDEFGH"}`), user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	ApproveFeishuPairingRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["status"] != "approved" {
		t.Fatalf("expected approved status, got %v", body["status"])
	}
}

func TestApproveFeishuPairingRequest_NotFound(t *testing.T) {
	inst, user := setupPairingEnv(t, func(cmd string) (string, string, uint32) {
		if !strings.Contains(cmd, "pairing") || !strings.Contains(cmd, "approve") || !strings.Contains(cmd, "ABCDEFGH") {
			return "", "unexpected command", 1
		}
		return "", "pairing code not found", 1
	})

	req := buildJSONRequest(t, "POST", "/api/v1/instances/1/pairing/feishu/approve", []byte(`{"code":"ABCDEFGH"}`), user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	ApproveFeishuPairingRequest(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Pairing code not found or expired") {
		t.Fatalf("expected not-found error, got %s", w.Body.String())
	}
}

func TestApproveFeishuPairingRequest_AlreadyApproved(t *testing.T) {
	inst, user := setupPairingEnv(t, func(cmd string) (string, string, uint32) {
		if !strings.Contains(cmd, "pairing") || !strings.Contains(cmd, "approve") || !strings.Contains(cmd, "ABCDEFGH") {
			return "", "unexpected command", 1
		}
		return "", "already approved", 1
	})

	req := buildJSONRequest(t, "POST", "/api/v1/instances/1/pairing/feishu/approve", []byte(`{"code":"ABCDEFGH"}`), user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	ApproveFeishuPairingRequest(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Pairing code already approved") {
		t.Fatalf("expected already-approved error, got %s", w.Body.String())
	}
}

func TestRevokeFeishuPairing_Success(t *testing.T) {
	inst, user := setupPairingEnv(t, func(cmd string) (string, string, uint32) {
		// Accept gateway stop command
		if strings.Contains(cmd, "gateway") && strings.Contains(cmd, "stop") {
			return "Gateway stopped", "", 0
		}
		return "", "unexpected command", 1
	})
	// Set Feishu config to verify it gets cleared
	inst.ChannelsConfig = `{"feishu":{"app_id":"test"}}`
	inst.FeishuAppSecret = "encrypted_secret"
	database.DB.Save(&inst)

	req := buildRequest(t, "POST", "/api/v1/instances/1/pairing/feishu/revoke", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	RevokeFeishuPairing(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["status"] != "deleted" {
		t.Fatalf("expected deleted status, got %v", body["status"])
	}
	// Verify database was updated
	var updatedInst database.Instance
	if err := database.DB.First(&updatedInst, inst.ID).Error; err != nil {
		t.Fatalf("failed to fetch updated instance: %v", err)
	}
	if updatedInst.ChannelsConfig != "" {
		t.Fatalf("expected channels_config to be empty, got %q", updatedInst.ChannelsConfig)
	}
	if updatedInst.FeishuAppSecret != "" {
		t.Fatalf("expected feishu_app_secret to be empty, got %q", updatedInst.FeishuAppSecret)
	}
}

func TestRevokeFeishuPairing_InstanceNotRunning(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-revoke-not-running", "Revoke Not Running")
	database.DB.Model(&inst).Update("status", "stopped")
	user := createTestUser(t, "admin")

	SSHMgr = sshproxy.NewSSHManager(nil, "")
	t.Cleanup(func() {
		SSHMgr.CloseAll()
		SSHMgr = nil
	})
	orchestrator.Set(&mockOrchestrator{})
	t.Cleanup(func() { orchestrator.Set(nil) })

	req := buildRequest(t, "POST", "/api/v1/instances/1/pairing/feishu/revoke", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	RevokeFeishuPairing(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not running") {
		t.Fatalf("expected not running error, got %s", w.Body.String())
	}
}


func TestRevokeFeishuPairing_GatewayStopFailed(t *testing.T) {
	inst, user := setupPairingEnv(t, func(cmd string) (string, string, uint32) {
		if strings.Contains(cmd, "gateway") && strings.Contains(cmd, "stop") {
			return "", "gateway error", 1
		}
		return "", "unexpected command", 1
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/pairing/feishu/revoke", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	RevokeFeishuPairing(w, req)

	// Gateway stop failure should still return 200 (DB was updated)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 even with gateway stop failure, got %d (body: %s)", w.Code, w.Body.String())
	}
}
