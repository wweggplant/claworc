package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

func TestCreateInstance_FeishuConfigDefaultsRenderMode(t *testing.T) {
	setupTestDB(t)
	orchestrator.Set(nil)
	defer orchestrator.Set(nil)

	user := createTestUser(t, "admin")
	req := buildJSONRequest(t, http.MethodPost, "/api/v1/instances", []byte(`{
		"display_name":"Feishu Create Test",
		"feishu_app_id":"cli_test"
	}`), user, nil)
	w := httptest.NewRecorder()

	CreateInstance(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d (body: %s)", w.Code, w.Body.String())
	}

	var inst database.Instance
	if err := database.DB.Last(&inst).Error; err != nil {
		t.Fatalf("load instance: %v", err)
	}

	var cfg database.ChannelsConfig
	if err := json.Unmarshal([]byte(inst.ChannelsConfig), &cfg); err != nil {
		t.Fatalf("unmarshal channels config: %v", err)
	}
	if cfg.Feishu == nil {
		t.Fatal("expected feishu config to be present")
	}
	if cfg.Feishu.Accounts.Default.RenderMode != defaultFeishuRenderMode {
		t.Fatalf("expected renderMode %q, got %q", defaultFeishuRenderMode, cfg.Feishu.Accounts.Default.RenderMode)
	}
}

func TestUpdateInstance_FeishuConfigDefaultsRenderMode(t *testing.T) {
	setupTestDB(t)
	orchestrator.Set(nil)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-feishu-update", "Feishu Update Test")
	user := createTestUser(t, "admin")
	req := buildJSONRequest(t, http.MethodPut, fmt.Sprintf("/api/v1/instances/%d", inst.ID), []byte(`{
		"feishu_app_id":"cli_test"
	}`), user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	UpdateInstance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var updated database.Instance
	if err := database.DB.First(&updated, inst.ID).Error; err != nil {
		t.Fatalf("reload instance: %v", err)
	}

	var cfg database.ChannelsConfig
	if err := json.Unmarshal([]byte(updated.ChannelsConfig), &cfg); err != nil {
		t.Fatalf("unmarshal channels config: %v", err)
	}
	if cfg.Feishu == nil {
		t.Fatal("expected feishu config to be present")
	}
	if cfg.Feishu.Accounts.Default.RenderMode != defaultFeishuRenderMode {
		t.Fatalf("expected renderMode %q, got %q", defaultFeishuRenderMode, cfg.Feishu.Accounts.Default.RenderMode)
	}
}
