# Live vision design candidate B: shared latest-frame JPEG hub

## Usage

A caller can read the current image or wait for the next image using only a managed device ID; neither call requires or refreshes a control lease. The cursor makes a wait precise across helper restarts:

```sh
agent-phone call frame '{"device":"<managed-udid>"}'
agent-phone call wait_frame '{"device":"<managed-udid>","afterEpoch":"<epoch>","afterSequence":42,"timeoutMs":5000}'
agent-phone watch --device <managed-udid>
```

The first two return frame metadata, state, and JPEG bytes (base64 in CLI JSON). `watch` is one persistent latest-only subscription used by the dashboard; it emits framed records, including status changes. The MCP tools `phone_frame({device})` and `phone_wait_frame({device, afterEpoch, afterSequence, timeoutMs})` return the same metadata and attach a JPEG image content block when one is available.

The dashboard keeps one `watch` process per visible managed phone. It decodes records off the main actor and publishes only the newest decoded image. Its card labels the last image `live`, `stale`, `disconnected`, or `error`; it keeps showing the last image with its age when capture stops.

## Problem

`Phone.mu` currently serializes lease validation, AX observation, and native actions. `observe` optionally forks `simctl screenshot` to a permanent evidence path, and `fleet` reads `lastObservation` only when it can `TryLock` the phone. Making either path the live stream would couple read-only viewing to action-reference lifetime, lease traffic, or full-resolution screenshot subprocesses. Four booted simulators on an 8 GB Mac also need a bounded producer and fan-out model. The research source shows persistent CoreSimulator framebuffer callbacks over IOSurface; that private host API still needs the separate runtime prototype and four-stream measurements described in `grounding.md`.

## Shape

### Types and signatures

```go
// A cursor is valid only within its epoch. A new helper attachment gets a new,
// unguessable epoch; sequence starts at 1 and increases for each published JPEG.
type FrameCursor struct {
    Epoch    string `json:"epoch"`
    Sequence uint64 `json:"sequence"`
}

type FrameStatus string // starting | live | stale | idle | disconnected | error

type LiveFrame struct {
    Device     string    `json:"device"`
    Cursor     FrameCursor `json:"cursor"`
    CapturedAt time.Time `json:"capturedAt"` // UTC, sampled from the IOSurface
    Width      int       `json:"width"`
    Height     int       `json:"height"`
    JPEG       []byte    `json:"jpeg"`
}

type FrameSnapshot struct {
    Status     FrameStatus `json:"status"`
    Cursor     FrameCursor `json:"cursor"` // current stream epoch/sequence, even before its first frame
    Frame      *LiveFrame  `json:"frame,omitempty"` // may be last frame while stale/disconnected
    UpdatedAt  time.Time   `json:"updatedAt"` // Go receive time; used to calculate staleness
    Error      string      `json:"error,omitempty"`
}

func (s *Service) ReadFrame(ctx context.Context, device string) (FrameSnapshot, error)
func (s *Service) WaitFrame(ctx context.Context, device string, after FrameCursor) (FrameSnapshot, error)
func (s *Service) SubscribeFrames(ctx context.Context, device string) (*FrameSubscription, error)
func (sub *FrameSubscription) Next(ctx context.Context) (FrameSnapshot, error)
func (sub *FrameSubscription) Close()
```

`ReadFrame` and `WaitFrame` take a temporary subscriber reference, return the current/newest frame or a status result, then release it. `SubscribeFrames` holds one reference until `Close`. All three share one per-device producer. `FrameSnapshot.Cursor` is the current stream cursor; a retained old `Frame` can have a different cursor after restart. `WaitFrame` returns immediately if its epoch differs from the current epoch or its sequence is behind; otherwise it waits until a frame or terminal status change wakes it, or the request context expires. A newly attached epoch begins at sequence zero, so a caller can wait for its first frame without confusing it with a cached image from the retired epoch.

Internally, `VisionHub` owns `map[DeviceID]*FrameStream`. Each stream has a small mutex, one immutable latest-frame pointer, status/error/timestamps, a subscriber count, one replaceable `changed` channel, and at most one native worker. A publish swaps the latest pointer and closes/replaces `changed`; readers wake and copy the latest snapshot. There are no per-reader frame queues. A slow reader therefore skips intermediate sequences instead of delaying capture or accumulating frames. `Next` advances from the caller's last cursor, not from an event queue.

The host worker is one `native/vision/main.swift` process per active simulator. It resolves the exact UDID, attaches to the main display's IOSurface callbacks, coalesces callbacks, and samples/encodes at no more than 4 fps. The callback only records the latest surface and signals a one-slot wakeup; one worker performs the readback and JPEG encode. The binary stdout protocol is length-prefixed and carries the UDID, capture timestamp, dimensions, and JPEG bytes. The Go reader rejects mismatched devices, invalid dimensions, and payloads over 2 MiB before publication. The native worker has one encode in flight and writes no files.

The hub admits at most four device slots, active or retaining an idle frame. A new device evicts the oldest idle slot; if all four are subscribed, another device gets a capacity error. Each slot retains one JPEG (up to 2 MiB) and permits at most eight active readers, bounding retained JPEGs at 8 MiB across the target fleet. Request/stream handlers hold at most one additional frame and have write deadlines. The worker scales down to a 1600-pixel long edge before encoding if needed. Staleness is derived from Go's receive time after two seconds without a frame; `capturedAt` remains the source sample time. Sequence gaps mean a reader skipped frames. Epoch changes mean the producing helper was reattached, including after reboot, and old cursors compare as changed.

### Module map and lifecycle

- `internal/phone/vision.go` owns `VisionHub`, per-device state, snapshots, cursor waits, subscription counts, and lifecycle transitions. It has no dependency on `Phone.mu`, `Bridge`, `Observation`, or evidence paths.
- `internal/phone/vision_native.go` starts/stops the host process, reads the bounded binary protocol, detects exit, and supervises retries. Capture starts on the first reader and stops when the last reader closes. On helper failure, the stream publishes `disconnected`, retries with bounded exponential backoff up to five attempts while subscribers remain, then publishes `error`; a new subscription or explicit resume can retry.
- `native/vision/main.swift` attaches to persistent framebuffer callbacks and emits latest JPEG records only. A tiny Objective-C exception boundary protects private CoreSimulator proxy calls. A host-native Makefile target builds this helper separately from the existing simulator guest action binary.
- `Service.Call` resolves the exact managed UDID first, then dispatches `frame` and `wait_frame` before locking the `Phone` or expiring/validating its lease. These methods never call `observe`, capture actionable targets, close the AX bridge, or mutate the current observation. Existing `act`/`observe` lease and action-reference semantics stay intact.
- The Unix HTTP server adds a long-lived `/vision/stream?device=...` route that calls `SubscribeFrames`; it writes `multipart/x-mixed-replace` records with frame headers and JPEG bodies, or a JSON status-only part when no image is available. It writes only the newest snapshot and applies a short write deadline, dropping/disconnecting slow clients. CLI `watch` bridges this route to stdout. MCP exposes one-shot `phone_frame` and cursor-based `phone_wait_frame` tools with image content blocks; it sends no unsolicited image notifications.
- `dashboard/AgentPhone.swift` subscribes only for visible managed cards. Its stream reader and image decode run off-main; a newest-only handoff bounds queued main-actor work. The existing five-second fleet poll remains for device/memory metadata, with screenshots removed from the live preview path.

Explicit shutdown and boot transitions stop the old worker and advance the epoch. A successful boot resumes capture only if readers remain. A worker-side CoreSimulator device-state notification exits capture when the simulator leaves Booted unexpectedly; the service reports disconnected and retries attachment for active readers. A stopped stream with no readers retains its last bounded JPEG as `idle`/stale; a new read starts capture and waits up to eight seconds for its first frame. `Service.Close` cancels all workers and waiters before closing the existing AX bridges. Control lease release/expiry does not alter stream subscribers.

The package hides CoreSimulator discovery, callback ownership, JPEG throttling, restarts, shared latest-slot synchronization, and per-client backpressure behind `ReadFrame`/`WaitFrame`/`SubscribeFrames`. Callers only see a device ID, cursor, frame metadata, bytes, and explicit state. HTTP/MCP wire details stay in `cmd/agent-phone`; CoreSimulator private protocol types stay in the native worker.

Before accepting the 4 fps and 2 MiB defaults, the prototype must report four-stream functional results, helper and service RSS, CPU/encode time, frame age and publish rate, largest JPEG, attach/restart latency, and behavior at the 8 GB memory budget. These are limits to measure, not performance claims.

## Synthesis decision

This is candidate B's recommendation, not an arena synthesis result. It selects one host-native callback producer and one shared latest JPEG slot per active phone so the dashboard, CLI, and MCP observe the same published frame sequence. Arena should choose the final base after comparing the other candidate.

## Tradeoffs accepted

- We accept JPEG encode/decode work and repeated wire transfer in exchange for one transport-neutral image representation shared by Go, CLI, SwiftUI, and MCP.
- We accept up to two seconds before declaring a stream stale in exchange for a simple threshold independent of dashboard refresh timing.
- We accept four active streams and eight readers per device as initial admission bounds in exchange for a finite capture and response-memory budget on the target Mac.
- We accept a private CoreSimulator host API and a separately built helper in exchange for event-driven capture without per-frame `simctl` polling or screenshots.
- We accept dropping intermediate frames for slow readers in exchange for bounded memory and a preview that converges to the current app screen.

## Alternatives considered

- **Direct IOSurface sharing with each client:** The native helper could pass/share the surface for direct SwiftUI rendering. This avoids JPEG conversion for the dashboard, but exposes surface lifetime, process-boundary sharing, and private API details to every display client. CLI/MCP would still need an encoder or another capture path, and separate consumers risk seeing different surface epochs. It has a broader caller surface and duplicates complexity at each reader, so it loses to one JPEG hub.
- **One native H.264/VideoToolbox stream:** A single encoder could feed all dashboard clients efficiently. It requires decoder and keyframe/reconnect state per reader; an MCP one-frame request would need to wait for/decode a keyframe or add a second JPEG encoder. That expands the public streaming protocol and makes cursor semantics depend on codec state. It may be reconsidered only if four-stream JPEG measurements exceed the resource budget.

## Open questions and risks

- Can the isolated native prototype attach to all four currently booted iOS 26.5 simulators using only host SDK/private frameworks available in the repository's no-global-dependency setup?
- Does the main-display callback reliably signal every visible content change on the supported Xcode version, and does the native worker receive device-state notifications quickly enough to retire an epoch during external shutdown/reboot?
- Do measured four-stream CPU and frame age justify 4 fps, 1600-pixel long edge, and a 2 MiB frame ceiling, or should the initial target be lower?
- Is a two-second stale label the right dashboard threshold when capture is throttled to 4 fps?

## Next implementation step

Build only the isolated host-native prototype, validate callback attach/detach and bounded JPEG output on all four simulators, and record resource/latency measurements before wiring the Go hub.
