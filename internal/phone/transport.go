package phone

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Adapted from jev-vphone-cli's persistent Simulator transport. Each Bridge
// belongs to one device and is used under that device's operation mutex.
type phoneBridge interface {
	Tree(context.Context) (map[string]any, error)
	Action(context.Context, map[string]any) (map[string]any, error)
	Close()
}

type Bridge struct {
	reader       *exec.Cmd
	actions      *exec.Cmd
	conn         net.Conn
	input        io.WriteCloser
	output       *bufio.Reader
	actionOutput *os.File
	socket       string
}

func NewBridge(ctx context.Context, udid, root string) (*Bridge, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	b := &Bridge{socket: filepath.Join(os.TempDir(), "ap-"+randomID()+".sock")}
	reader := filepath.Join(root, ".tools", "SimulatorFrameworkBridge-iOS")
	actions := filepath.Join(root, "build", "AgentPhoneActions")
	for _, p := range []string{reader, actions} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("missing helper %s; run make setup build", p)
		}
	}
	b.reader = exec.Command("/usr/bin/xcrun", "simctl", "spawn", udid, reader, "accessibility", "serve", b.socket, "--idle-timeout", "120", "--exit-on-disconnect", "true")
	if err := b.reader.Start(); err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			b.Close()
		}
	}()
	connect, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for contextError(connect) == nil {
		var err error
		b.conn, err = (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(connect, "unix", b.socket)
		if err == nil {
			break
		}
		select {
		case <-connect.Done():
			return nil, connect.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
	if err := contextError(connect); err != nil {
		return nil, err
	}
	if b.conn == nil {
		return nil, fmt.Errorf("native accessibility reader did not become ready")
	}
	b.actions = exec.Command("/usr/bin/xcrun", "simctl", "spawn", udid, actions)
	var err error
	b.input, err = b.actions.StdinPipe()
	if err != nil {
		return nil, err
	}
	pipe, err := b.actions.StdoutPipe()
	if err != nil {
		return nil, err
	}
	b.actionOutput = pipe.(*os.File)
	b.output = bufio.NewReaderSize(pipe, 64*1024)
	if err = b.actions.Start(); err != nil {
		return nil, err
	}
	if _, err = b.Action(ctx, map[string]any{"action": "prepare"}); err != nil {
		return nil, err
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	failed = false
	return b, nil
}

func (b *Bridge) Close() {
	if b.conn != nil {
		b.conn.Close()
	}
	if b.input != nil {
		b.input.Close()
	}
	if b.actionOutput != nil {
		b.actionOutput.Close()
	}
	for _, cmd := range []*exec.Cmd{b.reader, b.actions} {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	_ = os.Remove(b.socket)
}

func (b *Bridge) Tree(ctx context.Context) (map[string]any, error) {
	attrs := []string{"ElementType", "ElementBaseType", "Label", "Value", "Identifier", "Frame", "AutomationType", "Children", "PlaceholderValue"}
	for i := range attrs {
		attrs[i] = "XC_kAXXCAttribute" + attrs[i]
	}
	return b.Request(ctx, map[string]any{"verb": "describe", "method": "window-server", "x": 0, "y": 0, "snapshotTree": true, "automationMode": true, "attributes": attrs, "maxNodes": 20000})
}

func (b *Bridge) Request(ctx context.Context, request map[string]any) (out map[string]any, err error) {
	defer func() {
		if cutoff := contextError(ctx); cutoff != nil {
			out = nil
			err = cutoff
		}
	}()
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if err = contextError(ctx); err != nil {
		return nil, err
	}
	stop := cancelIO(ctx, func() { _ = b.conn.Close() })
	defer stop()
	_ = b.conn.SetDeadline(ioDeadline(ctx))
	payload := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(payload, uint32(len(data)))
	copy(payload[4:], data)
	if _, err = io.Copy(b.conn, bytes.NewReader(payload)); err != nil {
		return nil, err
	}
	header := make([]byte, 4)
	if _, err = io.ReadFull(b.conn, header); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header)
	if size == 0 || size > 16*1024*1024 {
		return nil, fmt.Errorf("invalid accessibility response length")
	}
	data = make([]byte, size)
	if _, err = io.ReadFull(b.conn, data); err != nil {
		return nil, err
	}
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result["ok"] != true {
		return nil, fmt.Errorf("accessibility: %v", result["error"])
	}
	if result["truncated"] == true {
		return nil, fmt.Errorf("accessibility tree is truncated; no input permitted")
	}
	return result, nil
}

func (b *Bridge) Action(ctx context.Context, request map[string]any) (out map[string]any, err error) {
	defer func() {
		if cutoff := contextError(ctx); cutoff != nil {
			out = nil
			err = cutoff
		}
	}()
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if err = contextError(ctx); err != nil {
		return nil, err
	}
	stop := cancelIO(ctx, func() { _ = b.input.Close(); _ = b.actionOutput.Close() })
	defer stop()
	if f, ok := b.input.(*os.File); ok {
		_ = f.SetWriteDeadline(ioDeadline(ctx))
	}
	_ = b.actionOutput.SetReadDeadline(ioDeadline(ctx))
	if _, err = b.input.Write(append(data, '\n')); err != nil {
		return nil, err
	}
	// ReadLine bounds responses; a failed stream is discarded by the caller.
	var response []byte
	for {
		part, more, readErr := b.output.ReadLine()
		if readErr != nil {
			return nil, readErr
		}
		response = append(response, part...)
		if len(response) > 2*1024*1024 {
			return nil, fmt.Errorf("native response too large")
		}
		if !more {
			break
		}
	}
	var result map[string]any
	if err = json.Unmarshal(response, &result); err != nil {
		return nil, err
	}
	if result["ok"] != true {
		return nil, fmt.Errorf("native input: %v", result["error"])
	}
	return result, nil
}

func ioDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(15 * time.Second)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	return deadline
}

// Joining a running callback prevents it from closing a later operation's stream.
func cancelIO(ctx context.Context, closeStream func()) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(done); closeStream() })
	return func() {
		if !stop() {
			<-done
		}
	}
}
