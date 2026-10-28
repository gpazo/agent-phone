package main

import (
	"agent-phone/internal/phone"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type requestCountingListener struct {
	net.Listener
	connections *atomic.Int64
}

func (l *requestCountingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &requestCountingConn{Conn: conn, connections: l.connections}, nil
}

type requestCountingConn struct {
	net.Conn
	connections *atomic.Int64
	seenRequest atomic.Bool
}

func (c *requestCountingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.seenRequest.CompareAndSwap(false, true) {
		c.connections.Add(1)
	}
	return n, err
}

func withFakeService(t *testing.T, handler http.Handler) *atomic.Int64 {
	t.Helper()

	data, err := os.MkdirTemp("/tmp", "ap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	t.Setenv("AGENT_PHONE_HOME", data)
	listener, err := net.Listen("unix", filepath.Join(data, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	connections := &atomic.Int64{}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(&requestCountingListener{Listener: listener, connections: connections}) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
	})
	t.Cleanup(closeRPCIdleConnections)
	return connections
}

func successfulRPCHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	})
}

func TestRequestReusesUnixConnectionAcrossSequentialCalls(t *testing.T) {
	listener := withFakeService(t, successfulRPCHandler())
	for i := 0; i < 12; i++ {
		result, err := request(context.Background(), "fleet", nil)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !result.OK {
			t.Fatalf("request %d returned unsuccessful response: %+v", i, result)
		}
	}
	if got := listener.Load(); got > 1 {
		t.Fatalf("12 completed sequential requests opened %d Unix connections, want at most 1", got)
	}
}

func TestRequestBoundsConnectionsAcrossConcurrentWaves(t *testing.T) {
	const workers = 8
	var firstWave atomic.Int64
	firstWaveReady := make(chan struct{})
	var closeFirstWave sync.Once
	var concurrentCalls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if concurrentCalls.Add(1) <= workers {
			if firstWave.Add(1) == workers {
				closeFirstWave.Do(func() { close(firstWaveReady) })
			}
			<-firstWaveReady
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	})
	listener := withFakeService(t, handler)

	for wave := 0; wave < 2; wave++ {
		var workersDone sync.WaitGroup
		workersDone.Add(workers)
		errs := make(chan error, workers)
		for i := 0; i < workers; i++ {
			go func() {
				defer workersDone.Done()
				result, err := request(context.Background(), "fleet", nil)
				if err != nil {
					errs <- err
				} else if !result.OK {
					errs <- os.ErrInvalid
				}
			}()
		}
		workersDone.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("wave %d request failed: %v", wave, err)
		}
	}

	if got := listener.Load(); got > workers {
		t.Fatalf("two waves of %d concurrent requests opened %d Unix connections, want at most %d", workers, got, workers)
	}
}

func TestMatrixFailureEnvelopeKeepsPartialResultInCLIAndMCP(t *testing.T) {
	listener := withFakeService(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":false,"result":{"completed":1},"error":{"code":"matrix_failed","message":"variant failed"}}`)
	}))
	cliOutput := captureStdout(t, func() error { return callCLI("matrix", `{}`) })
	var cliResponse map[string]any
	if err := json.Unmarshal(cliOutput, &cliResponse); err != nil {
		t.Fatalf("decode CLI output: %v", err)
	}
	cliResult, ok := cliResponse["result"].(map[string]any)
	if !ok || cliResult["completed"] != float64(1) {
		t.Fatalf("CLI output lost failed matrix partial result: %s", cliOutput)
	}
	if got := cliResponse["error"].(map[string]any)["code"]; got != "matrix_failed" {
		t.Fatalf("CLI output error code = %v, want matrix_failed", got)
	}

	harness := startMCP(t, request)
	harness.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"phone_matrix","arguments":{"matrix":"unused"}}}`)
	mcpResponse := harness.next()
	mcpOutput, _ := json.Marshal(mcpResponse)
	mcpResult, ok := mcpResponse["result"].(map[string]any)
	if !ok || mcpResult["isError"] != true {
		t.Fatalf("MCP did not flag failed matrix result: %s", mcpOutput)
	}
	content := mcpResult["content"].([]any)
	var response phone.Response
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &response); err != nil {
		t.Fatalf("decode MCP tool response: %v", err)
	}
	partial, ok := response.Result.(map[string]any)
	if !ok || partial["completed"] != float64(1) {
		t.Fatalf("MCP output lost failed matrix partial result: %s", mcpOutput)
	}
	if got := listener.Load(); got == 0 {
		t.Fatal("fake service received no requests")
	}
}

func captureStdout(t *testing.T, call func() error) []byte {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = write
	callErr := call()
	os.Stdout = old
	_ = write.Close()
	output, readErr := io.ReadAll(read)
	_ = read.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if callErr == nil {
		t.Fatal("callCLI returned nil for failed matrix response")
	}
	return output
}
