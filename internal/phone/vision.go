package phone

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const MaxFrameJPEG = 2 * 1024 * 1024
const MaxFrameMetadata = 8 * 1024
const MaxStreamRecord = 3 * 1024 * 1024

type FrameEpoch string

type FrameCursor struct {
	Epoch    FrameEpoch `json:"epoch"`
	Sequence uint64     `json:"sequence"`
}

type VisionFrame struct {
	Device     string      `json:"device"`
	Cursor     FrameCursor `json:"cursor"`
	CapturedAt time.Time   `json:"capturedAt"`
	Width      int         `json:"width"`
	Height     int         `json:"height"`
	JPEG       []byte      `json:"jpeg,omitempty"`
}

type FrameSnapshot struct {
	Device   string       `json:"device"`
	State    string       `json:"state"`
	Frame    *VisionFrame `json:"frame,omitempty"`
	Error    string       `json:"error,omitempty"`
	TimedOut bool         `json:"timedOut,omitempty"`
}

type frameSource func(context.Context, string, func(VisionFrame)) error

type framePolicy struct {
	idleGrace  time.Duration
	staleAfter time.Duration
	retryMin   time.Duration
	retryMax   time.Duration
}

var defaultFramePolicy = framePolicy{2 * time.Second, 3 * time.Second, 250 * time.Millisecond, 4 * time.Second}

type frameStream struct {
	device    string
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	changed   chan struct{}
	revision  uint64
	readers   int
	idle      *time.Timer
	snapshot  FrameSnapshot
	epoch     FrameEpoch
	accepting bool
}

type FrameHub struct {
	mu      sync.Mutex
	streams map[string]*frameStream
	paused  map[string]bool
	source  frameSource
	policy  framePolicy
	closed  bool
}

func newFrameHub(source frameSource) *FrameHub {
	return &FrameHub{streams: make(map[string]*frameStream), paused: make(map[string]bool), source: source, policy: defaultFramePolicy}
}

type FrameSubscription struct {
	hub      *FrameHub
	stream   *frameStream
	revision uint64
	once     sync.Once
	closed   chan struct{}
}

func (h *FrameHub) subscribe(ctx context.Context, device string) (*FrameSubscription, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return nil, fail("vision_closed", "Vision service is closed.")
		}
		if h.paused[device] {
			h.mu.Unlock()
			return nil, fail("vision_lifecycle", "Device lifecycle operation is in progress.")
		}
		e := h.streams[device]
		if e != nil && e.ctx.Err() != nil {
			done := e.done
			h.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		if e == nil {
			if len(h.streams) >= 4 {
				h.mu.Unlock()
				return nil, fail("vision_capacity", "At most four devices can stream at once.")
			}
			lifetime, cancel := context.WithCancel(context.Background())
			e = &frameStream{device: device, ctx: lifetime, cancel: cancel, done: make(chan struct{}), changed: make(chan struct{}), revision: 1, snapshot: FrameSnapshot{Device: device, State: "starting"}}
			h.streams[device] = e
			go h.run(e)
		}
		if e.readers >= 8 {
			h.mu.Unlock()
			return nil, fail("vision_readers", "At most eight readers may subscribe to one device.")
		}
		if e.idle != nil {
			e.idle.Stop()
			e.idle = nil
		}
		e.readers++
		sub := &FrameSubscription{hub: h, stream: e, closed: make(chan struct{})}
		h.mu.Unlock()
		go func() {
			select {
			case <-ctx.Done():
				sub.Close()
			case <-sub.closed:
			}
		}()
		return sub, nil
	}
}

func (s *FrameSubscription) Close() {
	s.once.Do(func() {
		close(s.closed)
		h, e := s.hub, s.stream
		h.mu.Lock()
		defer h.mu.Unlock()
		e.readers--
		if e.readers == 0 && e.ctx.Err() == nil {
			e.idle = time.AfterFunc(h.policy.idleGrace, func() {
				h.mu.Lock()
				defer h.mu.Unlock()
				if e.readers == 0 {
					e.cancel()
				}
			})
		}
	})
}

func (h *FrameHub) changed(e *frameStream) {
	e.revision++
	close(e.changed)
	e.changed = make(chan struct{})
}

func (s *FrameSubscription) snapshot() (FrameSnapshot, uint64, <-chan struct{}) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	value := s.stream.snapshot
	if s.stream.ctx.Err() != nil {
		value.State = "offline"
	}
	if value.State == "live" && value.Frame != nil && time.Since(value.Frame.CapturedAt) > s.hub.policy.staleAfter {
		value.State = "stale"
	}
	return value, s.stream.revision, s.stream.changed
}

// Next is used by one reader. Status changes wake it without advancing a frame cursor.
func (s *FrameSubscription) Next(ctx context.Context) (FrameSnapshot, error) {
	for {
		if err := ctx.Err(); err != nil {
			return FrameSnapshot{}, err
		}
		select {
		case <-s.closed:
			return FrameSnapshot{}, io.EOF
		default:
		}
		value, revision, changed := s.snapshot()
		if revision != s.revision {
			s.revision = revision
			return value, nil
		}
		select {
		case <-ctx.Done():
			return FrameSnapshot{}, ctx.Err()
		case <-s.closed:
			return FrameSnapshot{}, io.EOF
		case <-s.stream.done:
			return FrameSnapshot{}, io.EOF
		case <-changed:
		}
	}
}

func (s *FrameSubscription) Wait(ctx context.Context, after FrameCursor) (FrameSnapshot, error) {
	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				value, _, _ := s.snapshot()
				value.TimedOut = true
				value.Frame = nil
				return value, nil
			}
			return FrameSnapshot{}, err
		}
		select {
		case <-s.closed:
			if ctx.Err() != nil {
				continue
			}
			return FrameSnapshot{}, io.EOF
		default:
		}
		value, _, changed := s.snapshot()
		if value.State == "live" && value.Frame != nil && (value.Frame.Cursor.Epoch != after.Epoch || value.Frame.Cursor.Sequence > after.Sequence) {
			return value, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				value, _, _ = s.snapshot()
				value.TimedOut = true
				value.Frame = nil
				return value, nil
			}
			return FrameSnapshot{}, ctx.Err()
		case <-s.closed:
			if ctx.Err() != nil {
				continue
			}
			return FrameSnapshot{}, io.EOF
		case <-s.stream.done:
			value, _, _ = s.snapshot()
			return value, fail("vision_offline", "Frame stream stopped.")
		case <-changed:
		}
	}
}

func (h *FrameHub) run(e *frameStream) {
	defer func() {
		h.mu.Lock()
		e.accepting = false
		e.snapshot.State = "offline"
		if e.idle != nil {
			e.idle.Stop()
		}
		if h.streams[e.device] == e {
			delete(h.streams, e.device)
		}
		h.changed(e)
		close(e.done)
		h.mu.Unlock()
	}()
	delay := h.policy.retryMin
	for e.ctx.Err() == nil {
		epoch := FrameEpoch(randomID())
		h.mu.Lock()
		e.epoch, e.accepting = epoch, true
		h.mu.Unlock()
		attempt, cancel := context.WithCancel(e.ctx)
		result := make(chan error, 1)
		heartbeat := make(chan struct{}, 1)
		go func() {
			result <- h.source(attempt, e.device, func(frame VisionFrame) {
				h.mu.Lock()
				defer h.mu.Unlock()
				if !e.accepting || e.epoch != epoch || e.ctx.Err() != nil {
					return
				}
				previous := uint64(0)
				if e.snapshot.Frame != nil && e.snapshot.Frame.Cursor.Epoch == epoch {
					previous = e.snapshot.Frame.Cursor.Sequence
				}
				frame.Cursor = FrameCursor{epoch, previous + 1}
				frame.Device = e.device
				frame.JPEG = append([]byte(nil), frame.JPEG...)
				e.snapshot = FrameSnapshot{Device: e.device, State: "live", Frame: &frame}
				h.changed(e)
				select {
				case heartbeat <- struct{}{}:
				default:
				}
			})
		}()
		timer := time.NewTimer(h.policy.staleAfter)
		var err error
		finished := false
	wait:
		for {
			select {
			case <-e.ctx.Done():
				err = e.ctx.Err()
				break wait
			case err = <-result:
				finished = true
				break wait
			case <-timer.C:
				err = fmt.Errorf("capture source stalled")
				break wait
			case <-heartbeat:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(h.policy.staleAfter)
			}
		}
		timer.Stop()
		h.mu.Lock()
		e.accepting = false
		e.snapshot.State = "reconnecting"
		if !finished {
			e.snapshot.State = "stale"
		}
		if err != nil {
			e.snapshot.Error = err.Error()
		} else {
			e.snapshot.Error = "capture source ended"
		}
		h.changed(e)
		h.mu.Unlock()
		cancel()
		if !finished {
			<-result
		}
		retry := time.NewTimer(delay)
		select {
		case <-e.ctx.Done():
			retry.Stop()
			return
		case <-retry.C:
		}
		delay = min(delay*2, h.policy.retryMax)
	}
}

func (h *FrameHub) pause(device string) func() {
	h.mu.Lock()
	h.paused[device] = true
	e := h.streams[device]
	if e != nil {
		e.cancel()
	}
	h.mu.Unlock()
	if e != nil {
		<-e.done
	}
	return func() { h.mu.Lock(); delete(h.paused, device); h.mu.Unlock() }
}

func (h *FrameHub) Close() {
	h.mu.Lock()
	h.closed = true
	entries := make([]*frameStream, 0, len(h.streams))
	for _, e := range h.streams {
		e.cancel()
		entries = append(entries, e)
	}
	h.mu.Unlock()
	for _, e := range entries {
		<-e.done
	}
}

func (s *Service) SubscribeFrames(ctx context.Context, device string) (*FrameSubscription, error) {
	s.mu.Lock()
	_, managed := s.phones[device]
	hub := s.vision
	s.mu.Unlock()
	if !managed {
		return nil, fail("unmanaged_device", "Select an exact managed device ID from fleet.")
	}
	if hub == nil {
		return nil, fail("vision_unavailable", "Vision is unavailable.")
	}
	return hub.subscribe(ctx, device)
}

func (s *Service) frame(ctx context.Context, method string, a Params) (any, error) {
	timeout := 8 * time.Second
	after := FrameCursor{}
	if method == "wait_frame" {
		timeout = 20 * time.Second
		if a.TimeoutMs != nil {
			if *a.TimeoutMs < 1 || *a.TimeoutMs > 30000 {
				return nil, fail("invalid_timeout", "timeoutMs must be between 1 and 30000.")
			}
			timeout = time.Duration(*a.TimeoutMs) * time.Millisecond
		}
		_, epochErr := hex.DecodeString(a.AfterEpoch)
		if (a.AfterEpoch != "" && (len(a.AfterEpoch) != 32 || epochErr != nil)) || (a.AfterSequence > 0 && a.AfterEpoch == "") {
			return nil, fail("invalid_cursor", "Supply the epoch and sequence returned by a frame.")
		}
		after = FrameCursor{FrameEpoch(a.AfterEpoch), a.AfterSequence}
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sub, err := s.SubscribeFrames(wait, a.Device)
	var value FrameSnapshot
	if errors.Is(err, context.DeadlineExceeded) {
		value = FrameSnapshot{Device: a.Device, State: "starting", TimedOut: true, Error: "Waiting for the previous capture source to stop."}
		err = nil
	} else if err == nil {
		defer sub.Close()
		value, err = sub.Wait(wait, after)
	}
	if err == nil && method == "frame" && value.TimedOut {
		return nil, fail("vision_unavailable", "No fresh frame arrived within eight seconds: "+value.Error)
	}
	return value, err
}
