package phone

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type sourceFrameMetadata struct {
	Type       string    `json:"type"`
	Device     string    `json:"device"`
	Sequence   uint64    `json:"sequence"`
	CapturedAt time.Time `json:"capturedAt"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	JPEGBytes  int       `json:"jpegBytes"`
}

func readSourceFrame(reader io.Reader, device string, previous uint64, captured time.Time) (VisionFrame, uint64, error) {
	readPart := func(limit uint32) ([]byte, error) {
		var length uint32
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			return nil, err
		}
		if length == 0 || length > limit {
			return nil, fmt.Errorf("vision record length %d exceeds bound %d", length, limit)
		}
		data := make([]byte, length)
		_, err := io.ReadFull(reader, data)
		return data, err
	}
	raw, err := readPart(MaxFrameMetadata)
	if err != nil {
		return VisionFrame{}, 0, err
	}
	var m sourceFrameMetadata
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&m); err != nil {
		return VisionFrame{}, 0, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return VisionFrame{}, 0, fmt.Errorf("invalid metadata trailing data")
	}
	if m.Type != "frame" || !strings.EqualFold(m.Device, device) || m.Sequence <= previous || m.CapturedAt.IsZero() || !m.CapturedAt.After(captured) || m.CapturedAt.After(time.Now().Add(time.Second)) || time.Since(m.CapturedAt) > 3*time.Second || m.Width != 402 || m.Height < 1 || m.Height > 8192 || m.JPEGBytes < 1 || m.JPEGBytes > MaxFrameJPEG {
		return VisionFrame{}, 0, fmt.Errorf("invalid vision frame metadata")
	}
	data, err := readPart(MaxFrameJPEG)
	if err != nil {
		return VisionFrame{}, 0, err
	}
	if len(data) != m.JPEGBytes {
		return VisionFrame{}, 0, fmt.Errorf("vision JPEG length mismatch")
	}
	size, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || size.Width != m.Width || size.Height != m.Height {
		return VisionFrame{}, 0, fmt.Errorf("vision JPEG dimensions do not match metadata")
	}
	return VisionFrame{Device: device, CapturedAt: m.CapturedAt, Width: m.Width, Height: m.Height, JPEG: data}, m.Sequence, nil
}

type boundedDiagnostic struct {
	mu   sync.Mutex
	text []byte
}

func (b *boundedDiagnostic) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.text = append(b.text, p...)
	if len(b.text) > 4096 {
		b.text = append([]byte(nil), b.text[len(b.text)-4096:]...)
	}
	return n, nil
}
func (b *boundedDiagnostic) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.text))
}

func nativeFrameSource(root string) frameSource {
	return func(ctx context.Context, device string, emit func(VisionFrame)) error {
		cmd := exec.Command(filepath.Join(root, "build", "AgentPhoneVision"), device)
		diagnostics := &boundedDiagnostic{}
		cmd.Stderr = diagnostics
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			stdout.Close()
			return err
		}
		if err = cmd.Start(); err != nil {
			stdout.Close()
			stdin.Close()
			return err
		}
		defer stdout.Close()
		defer stdin.Close()
		exited := make(chan struct{})
		stop := make(chan struct{})
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			select {
			case <-ctx.Done():
			case <-stop:
			}
			_ = stdin.Close()
			_ = cmd.Process.Signal(syscall.SIGTERM)
			timer := time.NewTimer(750 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-exited:
			case <-timer.C:
				_ = cmd.Process.Kill()
				_ = stdout.Close()
			}
		}()
		var sequence uint64
		var captured time.Time
		for {
			frame, seq, readErr := readSourceFrame(stdout, device, sequence, captured)
			if readErr != nil {
				err = readErr
				break
			}
			if ctx.Err() != nil {
				err = ctx.Err()
				break
			}
			sequence, captured = seq, frame.CapturedAt
			emit(frame)
		}
		close(stop)
		waitErr := cmd.Wait()
		close(exited)
		<-stopped
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == io.EOF && waitErr != nil {
			err = waitErr
		}
		if err == nil {
			err = io.EOF
		}
		return fmt.Errorf("vision helper: %w: %s", err, diagnostics.String())
	}
}
