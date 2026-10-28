package main

import (
	"agent-phone/internal/phone"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/big"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

func mcpID(raw json.RawMessage) (string, bool) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return "", false
	}
	switch id := value.(type) {
	case string:
		return "string:" + id, true
	case json.Number:
		n, ok := new(big.Rat).SetString(string(id))
		if ok {
			return "number:" + n.RatString(), true
		}
	}
	return "", false
}

func mcp() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	defer closeRPCIdleConnections()
	return serveMCP(ctx, os.Stdin, os.Stdout, request)
}

func serveMCP(ctx context.Context, input io.Reader, output io.Writer, call func(context.Context, string, json.RawMessage) (phone.Response, error)) error {
	var outputMu, sessionsMu sync.Mutex
	var workers sync.WaitGroup
	pending := map[string]context.CancelFunc{}
	emit := func(id json.RawMessage, result any, rpcError any) {
		outputMu.Lock()
		defer outputMu.Unlock()
		response := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcError != nil {
			response["error"] = rpcError
		} else {
			response["result"] = result
		}
		_ = json.NewEncoder(output).Encode(response)
	}
	reject := func(id json.RawMessage, code int, message string) {
		emit(id, nil, map[string]any{"code": code, "message": message})
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 65536), 2*1024*1024)
	for scanner.Scan() {
		var m rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			reject(json.RawMessage(`null`), -32700, "Invalid JSON")
			continue
		}
		if len(m.ID) == 0 {
			if m.Method == "notifications/cancelled" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(m.Params, &p) == nil {
					if key, ok := mcpID(p.RequestID); ok {
						sessionsMu.Lock()
						cancel := pending[key]
						sessionsMu.Unlock()
						if cancel != nil {
							cancel()
						}
					}
				}
			}
			continue
		}
		key, ok := mcpID(m.ID)
		if !ok {
			reject(m.ID, -32600, "Request ID must be a number or string")
			continue
		}
		sessionsMu.Lock()
		_, duplicate := pending[key]
		sessionsMu.Unlock()
		if duplicate {
			reject(m.ID, -32600, "Request ID is already active")
			continue
		}
		switch m.Method {
		case "initialize":
			var p struct {
				Version string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(m.Params, &p)
			version := "2025-06-18"
			for _, supported := range []string{"2024-11-05", "2025-03-26", "2025-06-18"} {
				if p.Version == supported {
					version = supported
				}
			}
			emit(m.ID, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "agent-phone", "version": "0.1.0"}, "instructions": "Use explicit device IDs. Acquire a managed phone before observation, waits, or input. Each action consumes its observation. Waits return one satisfying observation; timeout diagnostics cannot authorize input. Native-hit is an AX center receipt, not pixel visibility."}, nil)
		case "ping":
			emit(m.ID, map[string]any{}, nil)
		case "tools/list":
			emit(m.ID, map[string]any{"tools": toolDefinitions()}, nil)
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil {
				reject(m.ID, -32602, "Invalid tool arguments")
				continue
			}
			known := false
			for _, tool := range toolDefinitions() {
				if tool["name"] == p.Name {
					known = true
					break
				}
			}
			if !known {
				reject(m.ID, -32602, "Unknown tool")
				continue
			}
			sessionsMu.Lock()
			if len(pending) >= 8 {
				sessionsMu.Unlock()
				reject(m.ID, -32000, "At most eight tool requests may be pending")
				continue
			}
			requestCtx, cancel := context.WithCancel(ctx)
			pending[key] = cancel
			sessionsMu.Unlock()
			workers.Add(1)
			go func(m rpcMessage, key, method string, args json.RawMessage) {
				defer workers.Done()
				defer func() { cancel(); sessionsMu.Lock(); delete(pending, key); sessionsMu.Unlock() }()
				result, err := call(requestCtx, method, args)
				if err != nil {
					result = phone.Result(nil, err)
				}
				emit(m.ID, map[string]any{"content": mcpContent(result), "isError": !result.OK}, nil)
			}(m, key, strings.TrimPrefix(p.Name, "phone_"), p.Arguments)
		default:
			reject(m.ID, -32601, "Method not found")
		}
	}
	sessionsMu.Lock()
	for _, cancel := range pending {
		cancel()
	}
	sessionsMu.Unlock()
	workers.Wait()
	return scanner.Err()
}
