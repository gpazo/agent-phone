package main

import (
	"agent-phone/internal/phone"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

func paths() (string, string) {
	exe, _ := os.Executable()
	root := filepath.Dir(filepath.Dir(exe))
	if v := os.Getenv("AGENT_PHONE_ROOT"); v != "" {
		root = v
	}
	home, _ := os.UserHomeDir()
	data := filepath.Join(home, "Library", "Application Support", "AgentPhone")
	if v := os.Getenv("AGENT_PHONE_HOME"); v != "" {
		data = v
	}
	return root, data
}
func main() {
	if len(os.Args) < 2 {
		help()
		return
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve()
	case "mcp":
		err = mcp()
	case "watch":
		err = watch(os.Args[2:])
	case "list":
		err = callCLI("fleet", "{}")
	case "catalog":
		err = callCLI("catalog", "{}")
	case "call":
		if len(os.Args) < 3 {
			err = fmt.Errorf("usage: agent-phone call METHOD '[JSON parameters]'")
		} else {
			params := "{}"
			if len(os.Args) > 3 {
				params = os.Args[3]
			}
			err = callCLI(os.Args[2], params)
		}
	case "help", "--help", "-h":
		help()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func help() {
	fmt.Print(`agent-phone — lightweight iOS phones for agents

  agent-phone list                         List phones, ownership and memory
  agent-phone catalog                      Available runtimes and device types
  agent-phone watch --device UDID           Persistent live frames as NDJSON
  agent-phone call METHOD '{...}'           Structured operation (JSON result)
  agent-phone mcp                          MCP server over standard input/output
  agent-phone serve                        Local Unix-socket service

Methods: fleet, catalog, create, acquire, release, boot, shutdown, configure,
         install, launch, observe, act, frame, wait_frame, build, matrix.

Frame reads require only an explicit managed device ID. Control operations
require a lease token. Create a
managed phone, then acquire it. Observe returns element references and optional
screenshots; act requires the observation ID and element reference.
The local service starts automatically. No model or API key is required.
See README.md for examples and the Wikipedia demonstration.
`)
}

var processRPCClient struct {
	sync.Mutex
	socket string
	client *http.Client
	trans  *http.Transport
}

func sharedRPCClient(socket string) *http.Client {
	processRPCClient.Lock()
	defer processRPCClient.Unlock()
	if processRPCClient.socket != socket {
		if processRPCClient.trans != nil {
			processRPCClient.trans.CloseIdleConnections()
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
			},
			MaxConnsPerHost:     8,
			MaxIdleConns:        8,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
		}
		processRPCClient.socket = socket
		processRPCClient.trans = transport
		processRPCClient.client = &http.Client{Transport: transport, Timeout: 30 * time.Minute}
	}
	return processRPCClient.client
}

func closeRPCIdleConnections() {
	processRPCClient.Lock()
	defer processRPCClient.Unlock()
	if processRPCClient.trans != nil {
		processRPCClient.trans.CloseIdleConnections()
	}
}

func serve() error {
	root, data := paths()
	if err := os.MkdirAll(data, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(data, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("agent-phone service is already running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	socket := filepath.Join(data, "control.sock")
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	_ = os.Chmod(socket, 0600)
	service, err := phone.NewService(root, data)
	if err != nil {
		return err
	}
	defer service.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vision/stream" {
			service.StreamFrames(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "POST" || r.URL.Path != "/rpc" {
			http.NotFound(w, r)
			return
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024))
		dec.DisallowUnknownFields()
		var request phone.Request
		if err := dec.Decode(&request); err != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(phone.Result(nil, err))
			return
		}
		callCtx, stop := context.WithTimeout(r.Context(), 30*time.Minute)
		defer stop()
		result, err := service.Call(callCtx, request)
		_ = json.NewEncoder(w).Encode(phone.Result(result, err))
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { <-ctx.Done(); _ = server.Close() }()
	fmt.Fprintf(os.Stderr, "agent-phone service ready: %s\n", socket)
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
func client() (*http.Client, error) {
	_, data := paths()
	socket := filepath.Join(data, "control.sock")
	probe, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
	if err == nil {
		probe.Close()
	} else {
		if err = os.MkdirAll(data, 0700); err != nil {
			return nil, err
		}
		log, err := os.OpenFile(filepath.Join(data, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		defer log.Close()
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "serve")
		cmd.Stdout = log
		cmd.Stderr = log
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err = cmd.Start(); err != nil {
			return nil, err
		}
		go cmd.Wait()
		ready := false
		for i := 0; i < 100; i++ {
			c, e := net.DialTimeout("unix", socket, 100*time.Millisecond)
			if e == nil {
				c.Close()
				ready = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			return nil, fmt.Errorf("service did not start; inspect %s", filepath.Join(data, "daemon.log"))
		}
	}
	return sharedRPCClient(socket), nil
}
func request(method string, params json.RawMessage) (phone.Response, error) {
	c, err := client()
	if err != nil {
		return phone.Response{}, err
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	data, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		return phone.Response{}, err
	}
	response, err := c.Post("http://localhost/rpc", "application/json", bytes.NewReader(data))
	if err != nil {
		return phone.Response{}, err
	}
	defer response.Body.Close()
	var result phone.Response
	err = json.NewDecoder(io.LimitReader(response.Body, 32*1024*1024)).Decode(&result)
	return result, err
}
func callCLI(method, params string) error {
	result, err := request(method, json.RawMessage(params))
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(result); err != nil {
		return err
	}
	if !result.OK {
		return result.Error
	}
	return nil
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func mcp() error {
	// MCP stdio uses one JSON-RPC message per line. Diagnostics never go to stdout.
	var output sync.Mutex
	var workers sync.WaitGroup
	emit := func(id json.RawMessage, result any, rpcError any) {
		output.Lock()
		defer output.Unlock()
		response := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcError != nil {
			response["error"] = rpcError
		} else {
			response["result"] = result
		}
		_ = json.NewEncoder(os.Stdout).Encode(response)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 2*1024*1024)
	for scanner.Scan() {
		var m rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			emit(json.RawMessage(`null`), nil, map[string]any{"code": -32700, "message": "Invalid JSON"})
			continue
		}
		if len(m.ID) == 0 {
			continue
		}
		workers.Add(1)
		go func(m rpcMessage) {
			defer workers.Done()
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
				emit(m.ID, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "agent-phone", "version": "0.1.0"}, "instructions": "Use explicit device IDs. Acquire a managed phone before mutation or observation. Each action consumes its observation; use the returned observation for subsequent input. Acknowledgment does not prove task completion."}, nil)
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
					emit(m.ID, nil, map[string]any{"code": -32602, "message": "Invalid tool arguments"})
					return
				}
				method := strings.TrimPrefix(p.Name, "phone_")
				known := false
				for _, tool := range toolDefinitions() {
					if tool["name"] == p.Name {
						known = true
					}
				}
				if !known {
					emit(m.ID, nil, map[string]any{"code": -32602, "message": "Unknown tool"})
					return
				}
				result, err := request(method, p.Arguments)
				if err != nil {
					result = phone.Result(nil, err)
				}
				content := mcpContent(result)
				emit(m.ID, map[string]any{"content": content, "isError": !result.OK}, nil)
			default:
				emit(m.ID, nil, map[string]any{"code": -32601, "message": "Method not found"})
			}
		}(m)
	}
	workers.Wait()
	closeRPCIdleConnections()
	return scanner.Err()
}
func screenshotPath(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if p, ok := m["screenshot"].(string); ok {
		return p
	}
	return screenshotPath(m["observation"])
}

func toolDefinitions() []map[string]any {
	descriptions := map[string]string{
		"fleet":      "List phones, session owners, cached observations, and measured memory. Existing unmanaged phones are read-only.",
		"catalog":    "List available Apple simulator runtimes and device types.",
		"create":     "Create and register an isolated simulator. Does not boot it.",
		"acquire":    "Acquire exclusive device control for an owner. Lease expires after 30 minutes of inactivity.",
		"release":    "Release device control and stop persistent observation helpers. Does not shut down the phone.",
		"boot":       "Boot a managed phone with a SimSlim profile, subject to the total simulator memory budget.",
		"shutdown":   "Shut down an explicitly leased managed phone.",
		"configure":  "Set a managed simulator's appearance or Dynamic Type size.",
		"install":    "Install an absolute simulator .app bundle on a leased phone.",
		"launch":     "Launch an app by bundle ID, with optional launch arguments.",
		"observe":    "Read native accessibility controls and optionally capture a screenshot. Returns timestamped element references; invalidates the previous observation.",
		"act":        "Perform one verified native action against an element reference. Requires its observation ID; returns fresh state. Never retry a mutation without reobserving.",
		"build":      "Serialize an Xcode simulator build with two compiler jobs. Returns app products, source revision and log path.",
		"frame":      "Read the latest fresh screen without a lease or changing action references. Starts a shared capture source if needed. Waits at most eight seconds.",
		"wait_frame": "Wait for a newer sampled frame cursor. Pixels may be unchanged. No lease needed. Returns timedOut when no fresh newer frame arrives.",
		"matrix":     "Build and execute a JSON variation matrix, queueing devices within capacity. Returns per-variant results and evidence paths.",
	}
	fields := map[string]any{}
	for _, k := range []string{"device", "name", "deviceType", "runtime", "owner", "lease", "observation", "ref", "action", "text", "app", "bundleId", "appearance", "contentSize", "project", "scheme", "configuration", "derivedData", "matrix", "afterEpoch"} {
		fields[k] = map[string]any{"type": "string"}
	}
	fields["afterSequence"] = map[string]any{"type": "integer", "minimum": 0}
	fields["timeoutMs"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 30000}
	fields["screenshot"] = map[string]any{"type": "boolean"}
	for _, k := range []string{"arguments", "except", "keep"} {
		fields[k] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	}
	fields["buildSettings"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
	allowed := map[string]string{
		"frame": "device", "wait_frame": "device afterEpoch afterSequence timeoutMs",
		"fleet": "", "catalog": "", "create": "name deviceType runtime", "acquire": "device owner",
		"release": "device lease", "boot": "device lease except keep", "shutdown": "device lease",
		"configure": "device lease appearance contentSize", "install": "device lease app",
		"launch": "device lease bundleId arguments", "observe": "device lease screenshot",
		"act":   "device lease observation ref action text screenshot",
		"build": "project scheme configuration derivedData buildSettings", "matrix": "matrix",
	}
	required := map[string]string{
		"frame": "device", "wait_frame": "device",
		"create": "name deviceType runtime", "acquire": "device owner", "release": "device lease",
		"boot": "device lease", "shutdown": "device lease", "configure": "device lease", "install": "device lease app",
		"launch": "device lease bundleId", "observe": "device lease", "act": "device lease observation ref action",
		"build": "project scheme", "matrix": "matrix",
	}
	fields["device"] = map[string]any{"type": "string", "description": "Exact managed simulator UDID from create or fleet."}
	fields["lease"] = map[string]any{"type": "string", "description": "Opaque token returned by acquire; never share between agents."}
	fields["action"] = map[string]any{"type": "string", "enum": []string{"press", "type", "increment", "decrement", "scroll-up", "scroll-down"}}
	fields["appearance"] = map[string]any{"type": "string", "enum": []string{"light", "dark"}}
	var result []map[string]any
	for _, name := range []string{"fleet", "catalog", "create", "acquire", "release", "boot", "shutdown", "configure", "install", "launch", "observe", "act", "frame", "wait_frame", "build", "matrix"} {
		properties := map[string]any{}
		for _, key := range strings.Fields(allowed[name]) {
			properties[key] = fields[key]
		}
		result = append(result, map[string]any{"name": "phone_" + name, "description": descriptions[name], "inputSchema": map[string]any{"type": "object", "properties": properties, "required": strings.Fields(required[name]), "additionalProperties": false}})
	}
	return result
}
