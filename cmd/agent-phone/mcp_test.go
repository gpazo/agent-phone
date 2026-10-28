package main

import (
	"agent-phone/internal/phone"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type mcpTestOutput struct{ responses chan map[string]any }

func (w mcpTestOutput) Write(p []byte) (int, error) {
	var m map[string]any
	if err := json.Unmarshal(p, &m); err != nil {
		return 0, err
	}
	w.responses <- m
	return len(p), nil
}

type mcpTestHarness struct {
	t         *testing.T
	input     *io.PipeWriter
	responses chan map[string]any
	done      chan error
	once      sync.Once
}

func startMCP(t *testing.T, call func(context.Context, string, json.RawMessage) (phone.Response, error)) *mcpTestHarness {
	t.Helper()
	reader, writer := io.Pipe()
	h := &mcpTestHarness{t: t, input: writer, responses: make(chan map[string]any, 32), done: make(chan error, 1)}
	go func() {
		h.done <- serveMCP(context.Background(), reader, mcpTestOutput{h.responses}, call)
		reader.Close()
	}()
	t.Cleanup(h.close)
	return h
}
func (h *mcpTestHarness) send(raw string) {
	h.t.Helper()
	if _, err := io.WriteString(h.input, raw+"\n"); err != nil {
		h.t.Fatal(err)
	}
}
func (h *mcpTestHarness) next() map[string]any {
	h.t.Helper()
	select {
	case r := <-h.responses:
		return r
	case <-time.After(time.Second):
		h.t.Fatal("MCP response timed out")
		return nil
	}
}
func (h *mcpTestHarness) close() {
	h.once.Do(func() {
		h.input.Close()
		select {
		case err := <-h.done:
			if err != nil {
				h.t.Error(err)
			}
		case <-time.After(time.Second):
			h.t.Error("MCP did not cancel workers at EOF")
		}
	})
}
func toolMessage(id string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"phone_wait","arguments":{}}}`, id)
}

func TestMCPUsesDefaultSIGTERMTermination(t *testing.T) {
	const childEnv = "AGENT_PHONE_MCP_SIGNAL_TEST_CHILD"
	if os.Getenv(childEnv) == "1" {
		if err := mcp(); err != nil {
			t.Fatal(err)
		}
		return
	}

	requestEntered := make(chan struct{})
	requestCancelled := make(chan struct{})
	withFakeService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestEntered)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		chunk := strings.Repeat(" ", 16*1024)
		for {
			select {
			case <-r.Context().Done():
				close(requestCancelled)
				return
			default:
			}
			if _, err := io.WriteString(w, chunk); err != nil {
				select {
				case <-r.Context().Done():
					close(requestCancelled)
				case <-time.After(time.Second):
				}
				return
			}
			flusher.Flush()
		}
	}))
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPUsesDefaultSIGTERMTermination$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var waitDone chan error
	waitStarted := false
	waited := false
	defer func() {
		_ = stdin.Close()
		if !waited {
			_ = cmd.Process.Kill()
			if waitStarted {
				select {
				case <-waitDone:
				case <-time.After(time.Second):
					t.Error("MCP subprocess did not stop during cleanup")
				}
			} else {
				_ = cmd.Wait()
			}
		}
	}()

	if _, err := io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	response := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		response <- struct {
			line string
			err  error
		}{line, err}
	}()
	select {
	case got := <-response:
		if got.err != nil {
			t.Fatalf("read MCP response: %v", got.err)
		}
		var message map[string]any
		if err := json.Unmarshal([]byte(got.line), &message); err != nil || message["id"] != float64(1) || message["result"] == nil {
			t.Fatalf("MCP did not return a valid ping response: %s (decode error: %v)", got.line, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MCP did not respond to ping")
	}

	if _, err := io.WriteString(stdin, toolMessage(`2`)+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requestEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("MCP did not reach the local service")
	}
	waitDone = make(chan error, 1)
	waitStarted = true
	go func() { waitDone <- cmd.Wait() }()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waitDone:
		waited = true
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("MCP process did not terminate from SIGTERM: %v", err)
		}
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok || status.Signal() != syscall.SIGTERM {
			t.Fatalf("MCP process exited for the wrong reason: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MCP process stayed alive after SIGTERM with standard input and output open")
	}
	select {
	case <-requestCancelled:
	case <-time.After(time.Second):
		t.Fatal("MCP process exit did not cancel the in-flight service request")
	}
}

func toolErrorCode(t *testing.T, m map[string]any) string {
	t.Helper()
	r := m["result"].(map[string]any)
	if r["isError"] != true {
		t.Fatalf("tool did not fail: %+v", m)
	}
	var envelope phone.Response
	if err := json.Unmarshal([]byte(r["content"].([]any)[0].(map[string]any)["text"].(string)), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Error.Code
}
func TestMCPCancelRegisteredBeforeWorkerStarts(t *testing.T) {
	scheduled := make(chan struct{})
	release := make(chan struct{})
	h := startMCP(t, func(ctx context.Context, _ string, _ json.RawMessage) (phone.Response, error) {
		close(scheduled)
		<-release
		<-ctx.Done()
		return phone.Response{}, ctx.Err()
	})
	h.send(toolMessage(`7`))
	h.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`)
	<-scheduled
	close(release)
	if code := toolErrorCode(t, h.next()); code != "cancelled" {
		t.Fatal(code)
	}
}
func TestMCPEOFCancelsPendingRequests(t *testing.T) {
	called := make(chan struct{})
	h := startMCP(t, func(ctx context.Context, _ string, _ json.RawMessage) (phone.Response, error) {
		close(called)
		<-ctx.Done()
		return phone.Response{}, ctx.Err()
	})
	h.send(toolMessage(`1`))
	<-called
	h.close()
	if code := toolErrorCode(t, h.next()); code != "cancelled" {
		t.Fatal(code)
	}
}
func TestMCPBoundedPendingDuplicateAndTypedIDs(t *testing.T) {
	started := make(chan struct{}, 8)
	h := startMCP(t, func(ctx context.Context, _ string, _ json.RawMessage) (phone.Response, error) {
		started <- struct{}{}
		<-ctx.Done()
		return phone.Response{}, ctx.Err()
	})
	h.send(toolMessage(`1`))
	<-started
	h.send(toolMessage(`1.0`))
	duplicate := h.next()
	if duplicate["error"] == nil {
		t.Fatalf("duplicate accepted: %+v", duplicate)
	}
	h.send(toolMessage(`"1"`))
	<-started
	for i := 2; i <= 7; i++ {
		h.send(toolMessage(fmt.Sprint(i)))
		<-started
	}
	h.send(toolMessage(`8`))
	if h.next()["error"] == nil {
		t.Fatal("overflow request accepted")
	}
	h.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"1"}}`)
	response := h.next()
	if response["id"] != "1" || toolErrorCode(t, response) != "cancelled" {
		t.Fatalf("wrong typed ID cancelled: %+v", response)
	}
	h.send(`{"jsonrpc":"2.0","id":9,"method":"ping"}`)
	if h.next()["id"] != float64(9) {
		t.Fatal("overflow blocked input reader")
	}
}
func TestRequestPropagatesContext(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	withFakeService(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := request(ctx, "wait", json.RawMessage(`{}`)); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not cancel")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("service context did not cancel")
	}
}
func TestWaitMCPTimeoutRetainsDiagnosticsAndSuccessAttachesScreenshot(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "frame.jpg")
	if err := os.WriteFile(path, []byte{0xff, 0xd8, 0xff, 0xd9}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			h := startMCP(t, func(context.Context, string, json.RawMessage) (phone.Response, error) {
				if success {
					return phone.Result(map[string]any{"observation": map[string]any{"screenshot": path}, "attempts": 1}, nil), nil
				}
				return phone.Result(map[string]any{"lastObservation": map[string]any{"id": "diagnostic"}, "satisfied": []int{}, "attempts": 2}, &phone.APIError{Code: "wait_timeout", Message: "deadline"}), nil
			})
			h.send(toolMessage(`1`))
			response := h.next()
			result := response["result"].(map[string]any)
			content := result["content"].([]any)
			if success {
				if result["isError"] != false || len(content) != 2 || content[1].(map[string]any)["type"] != "image" {
					t.Fatalf("screenshot missing: %+v", response)
				}
			} else {
				if toolErrorCode(t, response) != "wait_timeout" {
					t.Fatal(response)
				}
				var envelope phone.Response
				_ = json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &envelope)
				if envelope.Result.(map[string]any)["lastObservation"] == nil {
					t.Fatal("lost timeout diagnostics")
				}
			}
		})
	}
}
func TestWaitToolSchema(t *testing.T) {
	var schema map[string]any
	for _, tool := range toolDefinitions() {
		if tool["name"] == "phone_wait" {
			schema = tool["inputSchema"].(map[string]any)
		}
	}
	if schema == nil || schema["additionalProperties"] != false {
		t.Fatal("wait schema missing or loose")
	}
	properties := schema["properties"].(map[string]any)
	conditions := properties["conditions"].(map[string]any)
	if conditions["minItems"] != 1 || conditions["maxItems"] != 16 {
		t.Fatal("condition bounds differ")
	}
	item := conditions["items"].(map[string]any)
	props := item["properties"].(map[string]any)
	where := props["where"].(map[string]any)
	if item["additionalProperties"] != false || where["additionalProperties"] != false || where["minProperties"] != 1 || item["allOf"] == nil {
		t.Fatal("nested schema differs")
	}
	fields := where["properties"].(map[string]any)
	for _, key := range []string{"role", "label", "identifier", "value", "context", "action"} {
		if fields[key] == nil {
			t.Fatalf("missing selector %s", key)
		}
	}
	evidence := props["evidence"].(map[string]any)["enum"].([]string)
	if len(evidence) != 2 || evidence[0] != "tree" || evidence[1] != "native-hit" {
		t.Fatal("unsupported evidence exposed")
	}
}
