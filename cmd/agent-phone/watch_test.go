package main

import (
	"agent-phone/internal/phone"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPFrameContentDoesNotDuplicateBase64(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("jpeg content"))
	result := phone.Result(map[string]any{"device": "phone", "state": "live", "frame": map[string]any{"device": "phone", "jpeg": encoded, "width": 402, "height": 874, "capturedAt": "source time", "cursor": map[string]any{"epoch": "epoch", "sequence": 1}}}, nil)
	content := mcpContent(result)
	if len(content) != 2 {
		t.Fatalf("content=%+v", content)
	}
	text := content[0].(map[string]any)["text"].(string)
	if strings.Contains(text, encoded) || strings.Contains(text, `"jpeg"`) {
		t.Fatal("image duplicated in text")
	}
	if !strings.Contains(text, "source time") || !strings.Contains(text, "epoch") {
		t.Fatal("metadata missing")
	}
	image := content[1].(map[string]any)
	if image["data"] != encoded || image["mimeType"] != "image/jpeg" {
		t.Fatal("image content missing")
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), encoded) {
		t.Fatal("MCP formatting mutated RPC response")
	}
}

func TestVisionToolDefinitionsRequireNoLeaseAndBoundWait(t *testing.T) {
	found := 0
	for _, tool := range toolDefinitions() {
		if tool["name"] != "phone_frame" && tool["name"] != "phone_wait_frame" {
			continue
		}
		found++
		schema := tool["inputSchema"].(map[string]any)
		required := schema["required"].([]string)
		if len(required) != 1 || required[0] != "device" {
			t.Fatalf("unexpected required fields %v", required)
		}
		properties := schema["properties"].(map[string]any)
		if _, ok := properties["lease"]; ok {
			t.Fatal("vision exposed lease")
		}
		if tool["name"] == "phone_wait_frame" {
			timeout := properties["timeoutMs"].(map[string]any)
			if timeout["minimum"] != 1 || timeout["maximum"] != 30000 {
				t.Fatal("timeout bounds missing")
			}
		}
	}
	if found != 2 {
		t.Fatalf("vision tool count=%d", found)
	}
}
