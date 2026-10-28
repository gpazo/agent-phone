package phone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCapture struct {
	emit    func(VisionFrame)
	fail    chan error
	stopped chan struct{}
	finish  chan struct{}
}

type fakeVision struct {
	started  chan *fakeCapture
	active   atomic.Int32
	peak     atomic.Int32
	holdExit bool
}

func (f *fakeVision) source(ctx context.Context, device string, emit func(VisionFrame)) error {
	n := f.active.Add(1)
	for {
		previous := f.peak.Load()
		if n <= previous || f.peak.CompareAndSwap(previous, n) {
			break
		}
	}
	defer f.active.Add(-1)
	capture := &fakeCapture{emit: emit, fail: make(chan error, 1), stopped: make(chan struct{}), finish: make(chan struct{})}
	f.started <- capture
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-capture.fail:
	}
	close(capture.stopped)
	if f.holdExit {
		<-capture.finish
	}
	return err
}

func setupVision(t *testing.T) (*FrameHub, *fakeVision) {
	t.Helper()
	fake := &fakeVision{started: make(chan *fakeCapture, 16)}
	hub := newFrameHub(fake.source)
	hub.policy = framePolicy{40 * time.Millisecond, 500 * time.Millisecond, 20 * time.Millisecond, 100 * time.Millisecond}
	t.Cleanup(hub.Close)
	return hub, fake
}

func subscription(t *testing.T, hub *FrameHub, device string) *FrameSubscription {
	t.Helper()
	sub, err := hub.subscribe(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	return sub
}

func receiveCapture(t *testing.T, f *fakeVision) *fakeCapture {
	t.Helper()
	select {
	case capture := <-f.started:
		return capture
	case <-time.After(time.Second):
		t.Fatal("source did not start")
		return nil
	}
}

func (c *fakeCapture) sample(value byte) {
	c.emit(VisionFrame{CapturedAt: time.Now(), Width: 402, Height: 874, JPEG: []byte{value}})
}

func nextFrame(t *testing.T, sub *FrameSubscription, after FrameCursor) FrameSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, err := sub.Wait(ctx, after)
	if err != nil || value.TimedOut || value.Frame == nil || value.State != "live" {
		t.Fatalf("frame=%+v err=%v", value, err)
	}
	return value
}

func TestVisionSharesProducerAndSlowReaderGetsNewest(t *testing.T) {
	hub, fake := setupVision(t)
	fast := subscription(t, hub, "phone")
	slow := subscription(t, hub, "phone")
	capture := receiveCapture(t, fake)
	capture.sample(1)
	first := nextFrame(t, fast, FrameCursor{})
	for i := 2; i <= 100; i++ {
		capture.sample(byte(i))
	}
	latest, err := slow.Next(context.Background())
	if err != nil || latest.Frame.Cursor.Sequence != 100 || latest.Frame.JPEG[0] != 100 {
		t.Fatalf("slow reader did not converge: %+v %v", latest, err)
	}
	if latest.Frame.Cursor.Epoch != first.Frame.Cursor.Epoch || fake.active.Load() != 1 {
		t.Fatal("subscribers did not share producer and epoch")
	}
}

func TestFrameReadsIgnoreActionLockAndDoNotTouchLeaseOrObservation(t *testing.T) {
	hub, fake := setupVision(t)
	expired := &Lease{Token: "lease", Owner: "agent", Expires: time.Now().Add(-time.Hour)}
	observation := &Observation{ID: "action-reference"}
	p := &Phone{UDID: "PHONE", lease: expired, observation: observation, lastObservation: observation}
	service := &Service{vision: hub, phones: map[string]*Phone{"PHONE": p}, data: t.TempDir()}
	sub := subscription(t, hub, "PHONE")
	capture := receiveCapture(t, fake)
	capture.sample(7)
	p.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, err := service.Call(ctx, Request{Method: "frame", Params: Params{Device: "PHONE"}})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("frame read waited on action lock")
	}
	cancel()
	if p.lease != expired || p.observation != observation || p.lastObservation != observation {
		t.Error("vision changed control state")
	}
	p.mu.Unlock()
	first := nextFrame(t, sub, FrameCursor{})
	if first.Frame.Device != "PHONE" {
		t.Fatal("response did not preserve exact managed ID")
	}
	p.lease = &Lease{Token: "lease", Owner: "agent", Expires: time.Now().Add(time.Hour)}
	if _, err := service.Call(context.Background(), Request{Method: "release", Params: Params{Device: "PHONE", Lease: "lease"}}); err != nil {
		t.Fatal(err)
	}
	capture.sample(8)
	if nextFrame(t, sub, first.Frame.Cursor).Frame.JPEG[0] != 8 {
		t.Fatal("release stopped vision")
	}
	for _, method := range []string{"frame", "wait_frame"} {
		_, err := service.Call(context.Background(), Request{Method: method, Params: Params{Device: "phone"}})
		var api *APIError
		if !errors.As(err, &api) || api.Code != "unmanaged_device" {
			t.Fatalf("inexact ID accepted by %s: %v", method, err)
		}
	}
}

func TestVisionWaitTimeoutCancellationAndEpochRestart(t *testing.T) {
	hub, fake := setupVision(t)
	sub := subscription(t, hub, "phone")
	old := receiveCapture(t, fake)
	old.sample(1)
	first := nextFrame(t, sub, FrameCursor{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	timeout, err := sub.Wait(ctx, first.Frame.Cursor)
	cancel()
	if err != nil || !timeout.TimedOut || timeout.Frame != nil {
		t.Fatalf("timeout=%+v %v", timeout, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err = sub.Wait(ctx, FrameCursor{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	old.fail <- fmt.Errorf("source disconnected")
	newer := receiveCapture(t, fake)
	short, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	noNew, err := sub.Wait(short, FrameCursor{Epoch: "unknown", Sequence: 0})
	stop()
	if err != nil || !noNew.TimedOut || noNew.State == "live" {
		t.Fatalf("old bytes returned as new live capture: %+v %v", noNew, err)
	}
	newer.sample(2)
	second := nextFrame(t, sub, first.Frame.Cursor)
	if second.Frame.Cursor.Epoch == first.Frame.Cursor.Epoch || second.Frame.Cursor.Sequence != 1 {
		t.Fatal("restart did not establish new epoch")
	}
	old.sample(255)
	latest := nextFrame(t, sub, FrameCursor{})
	if latest.Frame.Cursor != second.Frame.Cursor || latest.Frame.JPEG[0] != 2 {
		t.Fatal("retired producer overwrote current epoch")
	}
}

func TestVisionIdleGraceReusesThenJoinsBeforeReattach(t *testing.T) {
	hub, fake := setupVision(t)
	fake.holdExit = true
	first := subscription(t, hub, "phone")
	capture := receiveCapture(t, fake)
	capture.sample(1)
	cursor := nextFrame(t, first, FrameCursor{}).Frame.Cursor
	first.Close()
	reused := subscription(t, hub, "phone")
	if got := nextFrame(t, reused, FrameCursor{}).Frame.Cursor; got != cursor {
		t.Fatal("grace period did not reuse frame")
	}
	reused.Close()
	select {
	case <-capture.stopped:
	case <-time.After(time.Second):
		t.Fatal("idle capture not stopped")
	}
	attached := make(chan *FrameSubscription, 1)
	go func() { sub, _ := hub.subscribe(context.Background(), "phone"); attached <- sub }()
	select {
	case <-attached:
		t.Fatal("reattached before old process joined")
	case <-time.After(20 * time.Millisecond):
	}
	close(capture.finish)
	var second *FrameSubscription
	select {
	case second = <-attached:
	case <-time.After(time.Second):
		t.Fatal("reattach blocked after join")
	}
	defer second.Close()
	newer := receiveCapture(t, fake)
	newer.sample(2)
	if nextFrame(t, second, cursor).Frame.Cursor.Epoch == cursor.Epoch {
		t.Fatal("reattach retained epoch")
	}
	if fake.peak.Load() != 1 {
		t.Fatal("overlapping producers")
	}
	close(newer.finish)
}

func TestVisionStallRetiresAndWakesWaiters(t *testing.T) {
	hub, fake := setupVision(t)
	hub.policy.staleAfter = 40 * time.Millisecond
	hub.policy.retryMin = 100 * time.Millisecond
	sub := subscription(t, hub, "phone")
	old := receiveCapture(t, fake)
	old.sample(3)
	first := nextFrame(t, sub, FrameCursor{})
	select {
	case <-old.stopped:
	case <-time.After(time.Second):
		t.Fatal("stalled source not stopped")
	}
	timeout, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	value, err := sub.Wait(timeout, FrameCursor{Epoch: "different"})
	cancel()
	if err != nil || !value.TimedOut || value.State != "stale" {
		t.Fatalf("stalled capture accepted as live: %+v %v", value, err)
	}
	newer := receiveCapture(t, fake)
	newer.sample(4)
	if nextFrame(t, sub, first.Frame.Cursor).Frame.Cursor.Epoch == first.Frame.Cursor.Epoch {
		t.Fatal("stall did not retire epoch")
	}
}

func TestWaitFrameDeadlineIncludesRetiringWorker(t *testing.T) {
	hub, fake := setupVision(t)
	fake.holdExit = true
	sub := subscription(t, hub, "phone")
	capture := receiveCapture(t, fake)
	sub.Close()
	select {
	case <-capture.stopped:
	case <-time.After(time.Second):
		t.Fatal("idle source was not canceled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); close(capture.finish) }()
	service := &Service{vision: hub, phones: map[string]*Phone{"phone": {UDID: "phone"}}}
	timeout := 10
	done := make(chan Response, 1)
	go func() {
		value, err := service.Call(ctx, Request{Method: "wait_frame", Params: Params{Device: "phone", TimeoutMs: &timeout}})
		done <- Result(value, err)
	}()
	select {
	case reply := <-done:
		value, ok := reply.Result.(FrameSnapshot)
		if !reply.OK || !ok || !value.TimedOut || value.Frame != nil || value.Device != "phone" {
			t.Fatalf("retirement timeout returned %+v", reply)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("wait_frame ignored its deadline while a previous worker was retiring")
	}
}

func TestWaitFrameDeadlineWinsOverSubscriptionCleanup(t *testing.T) {
	hub, _ := setupVision(t)
	service := &Service{vision: hub, phones: map[string]*Phone{"phone": {UDID: "phone"}}}
	timeout := 1
	for i := 0; i < 50; i++ {
		value, err := service.Call(context.Background(), Request{Method: "wait_frame", Params: Params{Device: "phone", TimeoutMs: &timeout}})
		snapshot, ok := value.(FrameSnapshot)
		if err != nil || !ok || !snapshot.TimedOut || snapshot.Frame != nil {
			t.Fatalf("deadline lost to automatic subscription cleanup: %+v %v", value, err)
		}
	}
}

func TestVisionLimitsLifecyclePauseAndClose(t *testing.T) {
	hub, fake := setupVision(t)
	var subs []*FrameSubscription
	for i := 0; i < 8; i++ {
		subs = append(subs, subscription(t, hub, "one"))
	}
	one := receiveCapture(t, fake)
	if _, err := hub.subscribe(context.Background(), "one"); err == nil {
		t.Fatal("reader limit not enforced")
	}
	for _, device := range []string{"two", "three", "four"} {
		subscription(t, hub, device)
		receiveCapture(t, fake)
	}
	if _, err := hub.subscribe(context.Background(), "five"); err == nil {
		t.Fatal("producer limit not enforced")
	}
	resume := hub.pause("one")
	if _, err := hub.subscribe(context.Background(), "one"); err == nil {
		t.Fatal("capture allowed during lifecycle operation")
	}
	select {
	case <-one.stopped:
	default:
		t.Fatal("pause did not join producer")
	}
	if _, err := subs[0].Wait(context.Background(), FrameCursor{}); err == nil {
		t.Fatal("retired stream did not wake waiter")
	}
	resume()
	subscription(t, hub, "one")
	receiveCapture(t, fake)
	hub.Close()
	if fake.active.Load() != 0 {
		t.Fatal("Close returned before capture workers joined")
	}
	if _, err := hub.subscribe(context.Background(), "one"); err == nil {
		t.Fatal("closed service accepted subscriber")
	}
}

func TestServiceCloseStopsVisionBeforeWaitingForActionLock(t *testing.T) {
	hub, fake := setupVision(t)
	p := &Phone{UDID: "phone"}
	service := &Service{vision: hub, phones: map[string]*Phone{"phone": p}}
	subscription(t, hub, "phone")
	capture := receiveCapture(t, fake)
	p.mu.Lock()
	closed := make(chan struct{})
	go func() { service.Close(); close(closed) }()
	select {
	case <-capture.stopped:
	case <-time.After(time.Second):
		t.Error("vision close waited on action lock")
	}
	p.mu.Unlock()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("service close blocked")
	}
	service.Close()
}

func TestVisionStreamDisconnectReleasesReader(t *testing.T) {
	hub, fake := setupVision(t)
	service := &Service{vision: hub, phones: map[string]*Phone{"phone": {UDID: "phone"}}}
	server := httptest.NewServer(http.HandlerFunc(service.StreamFrames))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "?device=phone")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(response.Body)
	var record FrameSnapshot
	if err = decoder.Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record.State != "starting" {
		t.Fatalf("initial state=%s", record.State)
	}
	capture := receiveCapture(t, fake)
	capture.sample(1)
	if err = decoder.Decode(&record); err != nil || record.Frame == nil {
		t.Fatalf("frame decode %v %+v", err, record)
	}
	response.Body.Close()
	select {
	case <-capture.stopped:
	case <-time.After(time.Second):
		t.Fatal("client disconnect retained capture")
	}
	response, err = server.Client().Get(server.URL + "?device=unmanaged")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("unmanaged stream accepted")
	}
}

func TestFrameBoundsAndSubscriptionClose(t *testing.T) {
	hub, _ := setupVision(t)
	service := &Service{vision: hub, phones: map[string]*Phone{"phone": {UDID: "phone"}}}
	for _, value := range []int{0, -1, 30001} {
		_, err := service.Call(context.Background(), Request{Method: "wait_frame", Params: Params{Device: "phone", TimeoutMs: &value}})
		if err == nil {
			t.Fatalf("accepted timeout %d", value)
		}
	}
	_, err := service.Call(context.Background(), Request{Method: "wait_frame", Params: Params{Device: "phone", AfterSequence: 1}})
	if err == nil {
		t.Fatal("accepted cursor without epoch")
	}
	sub := subscription(t, hub, "phone")
	sub.Close()
	if _, err := sub.Next(context.Background()); err != io.EOF {
		t.Fatalf("closed subscriber returned %v", err)
	}
}

func TestCanceledSubscriptionReleasesLastReader(t *testing.T) {
	hub, fake := setupVision(t)
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := hub.subscribe(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	capture := receiveCapture(t, fake)
	cancel()
	select {
	case <-capture.stopped:
	case <-time.After(time.Second):
		t.Fatal("canceled subscription retained producer")
	}
}
