package main

import (
	"agent-phone/internal/phone"
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func watch(args []string) error {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	device := flags.String("device", "", "Exact managed device UDID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *device == "" && flags.NArg() == 1 {
		*device = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return fmt.Errorf("usage: agent-phone watch --device UDID")
	}
	if *device == "" {
		return fmt.Errorf("usage: agent-phone watch --device UDID")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	base, err := client(ctx)
	if err != nil {
		return err
	}
	c := &http.Client{Transport: base.Transport.(*http.Transport).Clone()}
	defer c.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/vision/stream?device="+url.QueryEscape(*device), nil)
	if err != nil {
		return err
	}
	response, err := c.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return fmt.Errorf("watch: %s: %s", response.Status, data)
	}
	fd := int(os.Stdout.Fd())
	oldFlags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if errno != 0 {
		return errno
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return err
	}
	defer syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, oldFlags)
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), phone.MaxStreamRecord)
	for scanner.Scan() {
		data := append(scanner.Bytes(), '\n')
		for len(data) > 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, err := syscall.Write(fd, data)
			if n > 0 {
				data = data[n:]
			}
			if err == syscall.EINTR {
				continue
			}
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(25 * time.Millisecond):
				}
				continue
			}
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
		}
	}
	return scanner.Err()
}

func mcpContent(result phone.Response) []any {
	var image string
	if result.OK {
		if value, ok := result.Result.(map[string]any); ok {
			if frame, ok := value["frame"].(map[string]any); ok {
				if encoded, ok := frame["jpeg"].(string); ok {
					if raw, err := base64.StdEncoding.DecodeString(encoded); err == nil && len(raw) <= phone.MaxFrameJPEG {
						image = encoded
					}
					copyFrame := make(map[string]any, len(frame))
					for key, value := range frame {
						if key != "jpeg" {
							copyFrame[key] = value
						}
					}
					copyValue := make(map[string]any, len(value))
					for key, value := range value {
						copyValue[key] = value
					}
					copyValue["frame"] = copyFrame
					result.Result = copyValue
				}
			}
		}
	}
	text, _ := json.Marshal(result)
	content := []any{map[string]any{"type": "text", "text": string(text)}}
	if image != "" {
		content = append(content, map[string]any{"type": "image", "mimeType": "image/jpeg", "data": image})
	}
	if result.OK && image == "" {
		if path := screenshotPath(result.Result); path != "" {
			if data, err := os.ReadFile(path); err == nil && len(data) <= 8*1024*1024 {
				content = append(content, map[string]any{"type": "image", "mimeType": "image/jpeg", "data": base64.StdEncoding.EncodeToString(data)})
			}
		}
	}
	return content
}
