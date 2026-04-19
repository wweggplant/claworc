package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// ChatResult holds timing data for a single chat interaction.
type ChatResult struct {
	InstanceID int
	TTFT       float64 // ms to first assistant token
	TotalMs    float64 // ms from send to lifecycle end
	Error      error
}

// runSingleChat opens a WebSocket to an instance chat endpoint, sends one
// message, and waits for the first assistant stream token (TTFT) and lifecycle
// end (total). It matches frames by runId for concurrency safety.
func runSingleChat(client *http.Client, instanceID int, message string, timeout time.Duration) ChatResult {
	result := ChatResult{InstanceID: instanceID}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	wsURL := buildWSURL(instanceID)
	wsHeaders := buildWSHeaders(client)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: wsHeaders,
	})
	if err != nil {
		result.Error = fmt.Errorf("ws dial: %w", err)
		return result
	}
	defer conn.CloseNow()

	// Step 1: wait for {"type":"connected"}
	_, data, err := conn.Read(ctx)
	if err != nil {
		result.Error = fmt.Errorf("read connected: %w", err)
		return result
	}
	var connected map[string]interface{}
	if err := json.Unmarshal(data, &connected); err != nil || connected["type"] != "connected" {
		result.Error = fmt.Errorf("expected connected, got: %s", string(data))
		return result
	}

	// Step 2: send chat message
	sendStart := time.Now()
	chatMsg, _ := json.Marshal(map[string]string{
		"type":    "chat",
		"content": message,
	})
	if err := conn.Write(ctx, websocket.MessageText, chatMsg); err != nil {
		result.Error = fmt.Errorf("write chat: %w", err)
		return result
	}

	// Step 3: read frames, track by runId
	var myRunID string
	ttftRecorded := false

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if !ttftRecorded {
				result.Error = fmt.Errorf("read before ttft: %w", err)
			}
			if result.TTFT > 0 && result.TotalMs == 0 {
				result.TotalMs = float64(time.Since(sendStart).Milliseconds())
			}
			return result
		}

		var frame struct {
			Event   string                 `json:"event"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}

		if frame.Event != "agent" {
			continue
		}

		payload := frame.Payload
		stream, _ := payload["stream"].(string)
		runID, _ := payload["runId"].(string)

		if runID == "" {
			continue
		}

		if myRunID == "" {
			myRunID = runID
		}
		if runID != myRunID {
			continue
		}

		if stream == "assistant" && !ttftRecorded {
			result.TTFT = float64(time.Since(sendStart).Milliseconds())
			ttftRecorded = true
		}

		if stream == "lifecycle" {
			dataMap, _ := payload["data"].(map[string]interface{})
			phase, _ := dataMap["phase"].(string)
			if phase == "end" {
				result.TotalMs = float64(time.Since(sendStart).Milliseconds())
				return result
			}
		}
	}
}

func buildWSURL(instanceID int) string {
	scheme := "ws"
	host := *server
	if strings.HasPrefix(host, "https://") {
		scheme = "wss"
	}
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	return fmt.Sprintf("%s://%s/api/v1/instances/%d/chat", scheme, host, instanceID)
}

func buildWSHeaders(client *http.Client) http.Header {
	h := http.Header{}
	u, err := url.Parse(*server)
	if err != nil || client.Jar == nil {
		return h
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == "claworc_session" {
			h.Set("Cookie", fmt.Sprintf("%s=%s", c.Name, c.Value))
			break
		}
	}
	return h
}
