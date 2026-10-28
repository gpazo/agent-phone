package phone

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testSourceRecord() (sourceFrameMetadata, []byte) {
	var data bytes.Buffer
	_ = jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 402, 10)), nil)
	jpeg := data.Bytes()
	return sourceFrameMetadata{Type: "frame", Device: "abcdef01-2345-6789-abcd-ef0123456789", Sequence: 1, CapturedAt: time.Now().UTC(), Width: 402, Height: 10, JPEGBytes: len(jpeg)}, jpeg
}

func sourceRecord(metadata sourceFrameMetadata, jpeg []byte) []byte {
	raw, _ := json.Marshal(metadata)
	var record bytes.Buffer
	_ = binary.Write(&record, binary.BigEndian, uint32(len(raw)))
	record.Write(raw)
	_ = binary.Write(&record, binary.BigEndian, uint32(len(jpeg)))
	record.Write(jpeg)
	return record.Bytes()
}

func TestSourceFrameValidatesIdentityTimeDimensionsAndLengths(t *testing.T) {
	metadata, jpeg := testSourceRecord()
	frame, sequence, err := readSourceFrame(bytes.NewReader(sourceRecord(metadata, jpeg)), strings.ToUpper(metadata.Device), 0, time.Time{})
	if err != nil || sequence != 1 || frame.Device != strings.ToUpper(metadata.Device) || !frame.CapturedAt.Equal(metadata.CapturedAt) {
		t.Fatalf("valid record rejected: %+v %v", frame, err)
	}
	cases := map[string]func(*sourceFrameMetadata){
		"identity":        func(m *sourceFrameMetadata) { m.Device = "other" },
		"type":            func(m *sourceFrameMetadata) { m.Type = "status" },
		"replay":          func(m *sourceFrameMetadata) { m.Sequence = 0 },
		"missing time":    func(m *sourceFrameMetadata) { m.CapturedAt = time.Time{} },
		"future":          func(m *sourceFrameMetadata) { m.CapturedAt = time.Now().Add(time.Hour) },
		"stale":           func(m *sourceFrameMetadata) { m.CapturedAt = time.Now().Add(-time.Hour) },
		"width":           func(m *sourceFrameMetadata) { m.Width = 401 },
		"height":          func(m *sourceFrameMetadata) { m.Height = 11 },
		"oversize height": func(m *sourceFrameMetadata) { m.Height = 8193 },
		"length":          func(m *sourceFrameMetadata) { m.JPEGBytes++ },
		"oversize image":  func(m *sourceFrameMetadata) { m.JPEGBytes = MaxFrameJPEG + 1 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := metadata
			change(&m)
			if _, _, err := readSourceFrame(bytes.NewReader(sourceRecord(m, jpeg)), metadata.Device, 0, time.Time{}); err == nil {
				t.Fatal("invalid frame accepted")
			}
		})
	}
	for name, data := range map[string][]byte{"truncated": sourceRecord(metadata, jpeg)[:20], "bad jpeg": sourceRecord(metadata, bytes.Repeat([]byte("x"), len(jpeg))), "invalid json": {0, 0, 0, 1, '{'}, "oversize metadata": {0, 0, 32, 1}, "empty metadata": {0, 0, 0, 0}} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readSourceFrame(bytes.NewReader(data), metadata.Device, 0, time.Time{}); err == nil {
				t.Fatal("malformed record accepted")
			}
		})
	}
	if _, _, err := readSourceFrame(bytes.NewReader(sourceRecord(metadata, jpeg)), metadata.Device, 1, time.Time{}); err == nil {
		t.Fatal("source sequence replay accepted")
	}
	if _, _, err := readSourceFrame(bytes.NewReader(sourceRecord(metadata, jpeg)), metadata.Device, 0, metadata.CapturedAt); err == nil {
		t.Fatal("source timestamp replay accepted")
	}
	raw, _ := json.Marshal(metadata)
	for _, bad := range [][]byte{append(raw, []byte("{}")...), bytes.Replace(raw, []byte(`"type"`), []byte(`"unknown"`), 1)} {
		var record bytes.Buffer
		_ = binary.Write(&record, binary.BigEndian, uint32(len(bad)))
		record.Write(bad)
		if _, _, err := readSourceFrame(&record, metadata.Device, 0, time.Time{}); err == nil {
			t.Fatal("extra metadata accepted")
		}
	}
	var oversized bytes.Buffer
	_ = binary.Write(&oversized, binary.BigEndian, uint32(len(raw)))
	oversized.Write(raw)
	_ = binary.Write(&oversized, binary.BigEndian, uint32(MaxFrameJPEG+1))
	if _, _, err := readSourceFrame(&oversized, metadata.Device, 0, time.Time{}); err == nil {
		t.Fatal("oversized JPEG allocation accepted")
	}
}

func TestMain(m *testing.M) {
	switch os.Getenv("AGENT_PHONE_TEST_VISION_HELPER") {
	case "stall":
		signal.Ignore(syscall.SIGTERM)
		_ = os.WriteFile(os.Getenv("AGENT_PHONE_TEST_VISION_READY"), []byte("ready"), 0600)
		os.Stdout.Write([]byte{0, 0, 0, 1})
		for {
			time.Sleep(time.Second)
		}
	case "valid":
		metadata, jpeg := testSourceRecord()
		metadata.Device = os.Args[1]
		os.Stdout.Write(sourceRecord(metadata, jpeg))
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "malformed":
		os.Stderr.WriteString(strings.Repeat("diagnostic\n", 1000))
		os.Stdout.Write([]byte{0, 0, 32, 1})
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNativeWorkerStopsAndJoinsOnCancellationOrMalformedRecord(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "build"), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(executable, filepath.Join(root, "build", "AgentPhoneVision")); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "stall", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT_PHONE_TEST_VISION_HELPER", mode)
			ready := filepath.Join(t.TempDir(), "ready")
			t.Setenv("AGENT_PHONE_TEST_VISION_READY", ready)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			emitted := make(chan VisionFrame, 1)
			done := make(chan error, 1)
			go func() {
				done <- nativeFrameSource(root)(ctx, "abcdef01-2345-6789-abcd-ef0123456789", func(frame VisionFrame) { emitted <- frame })
			}()
			if mode == "valid" {
				select {
				case <-emitted:
				case <-time.After(2 * time.Second):
					t.Fatal("helper frame missing")
				}
			}
			if mode == "stall" {
				deadline := time.Now().Add(2 * time.Second)
				for {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("stall helper did not start")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			if mode != "malformed" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("source ended without status")
				}
				if len(err.Error()) > 5000 {
					t.Fatal("unbounded helper diagnostics")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("helper failed to stop and join")
			}
		})
	}
}
