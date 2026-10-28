# Type/signature sketch

Usage in `usage-and-lifecycle.md` is the contract. This is pseudocode, deliberately not production source. Wire headers and private CoreSimulator objects stay behind the domain boundary.

## Go (`internal/phone/live.go`)

```go
// Existing phone.Frame remains AX geometry.
type LiveState uint8 // starting/live/idle/disconnected/stale/unsupported/closed
type LiveReason uint8 // current/changed/reset/status/timeout
type LiveCursor struct { opaque string } // constructed/parsed inside live.go

type LiveImage struct {
    epoch string
    sequence uint64
    sampledAt, receivedAt time.Time
    width, height, sourceWidth, sourceHeight int
    rotation int
    jpeg []byte // immutable; never returned as a mutable public slice
}
func (*LiveImage) Metadata() ImageMetadata { panic("not implemented") }
func (*LiveImage) WriteJPEG(io.Writer) error { panic("not implemented") }

type ImageMetadata struct {
    Epoch string
    Sequence uint64
    SampledAt, ReceivedAt time.Time
    Width, Height, SourceWidth, SourceHeight, Rotation int
}
type LiveUpdate struct {
    Device string
    State LiveState
    Reason LiveReason
    Cursor LiveCursor
    Image *LiveImage // nil on timeout or a retired/no-image state
    CheckedAt time.Time
    ErrorCode, ErrorMessage string
}
func (LiveCursor) String() string { panic("not implemented") }
type liveQuery struct { after LiveCursor; wait time.Duration }

// One public streaming entry, adapted by main.go to the existing Unix HTTP server.
// Performs exact managed-ID validation; it never takes Phone.mu.
func (*Service) WatchLive(context.Context, string) (*LiveReader, error) {
    panic("not implemented")
}
type LiveReader struct { /* manager, device, demand ID, last revision, once-close */ }
func (*LiveReader) Next(context.Context) (LiveUpdate, error) { panic("not implemented") }
func (*LiveReader) Close() error { panic("not implemented") } // idempotent

// Unexported capability received only after s.phones exact lookup.
type managedPhone struct { udid string }
type captureEpoch string
type feed struct { // owned entirely by the manager loop
    device managedPhone
    epoch captureEpoch
    latest *LiveImage
    state LiveState
    statusRevision uint64
    checkedAt time.Time // monotonic Go receipt retained for staleness
    readers int
    changed chan struct{} // close and replace; notification, never a frame queue
    idleAt, retryAt time.Time
    suspended bool
}
type liveManager struct {
    // One goroutine owns active map, pending start/stop state, deadlines and process.
    // No registry clone: active map contains feeds, not managed-device authority.
    commands chan liveCommand // bounded; no caller holds Phone.mu waiting on IPC
    events chan nativeEvent // bounded; reader backpressure ends in latest-slot drop
    done chan struct{}
    closeOnce sync.Once
}
func newLiveManager(root string) *liveManager { panic("not implemented") }
func (*liveManager) read(context.Context, managedPhone, liveQuery) (LiveUpdate, error) {
    // TODO validate query/admit demand; snapshot+change channel obtained atomically;
    // wait outside manager; always remove demand; avoid missed wakeups.
    panic("not implemented")
}
func (*liveManager) watch(context.Context, managedPhone) (*LiveReader, error) {
    panic("not implemented")
}
func (*liveManager) suspend(managedPhone) {
    // TODO acknowledge epoch retirement immediately; asynchronously detach.
    panic("not implemented")
}
func (*liveManager) resume(managedPhone) { panic("not implemented") }
func (*liveManager) close() error { panic("not implemented") }

// Private actor commands and native record structs are not public API.
// Keep native decoder, process owner and feed transition logic in this module.
func decodeNativeRecord(io.Reader) (nativeEvent, error) { panic("not implemented") }
func acceptFrame(feed, nativeEvent) (feed, bool) {
    // TODO pure validation/transition: matching device+epoch, geometry, timestamps,
    // sequence/hash expectation, <=1 MiB payload, supported type/version.
    panic("not implemented")
}
```

`Service.Call` retains its current request envelope. Add only `After string` and `WaitMS *int` to `Params` (a pointer distinguishes absent from zero), parse them into `liveQuery`, then call `read` after the current managed-phone lookup. All non-vision cases retain the current `p.mu`/lease path. `WatchLive` reuses that exact registry lookup helper. Do not add live cursors to `Observation` or accept them in `act`.

`cmd/agent-phone/main.go` owns serialization: RPC converts `LiveUpdate` to bounded JSON, MCP strips encoded image from text and emits one image content block, `/vision` writes private framed records. Serialization gets immutable image bytes through a writer, not through an exported mutable buffer. The HTTP adapter bounds both send time and total reader count. `fleet` returns state/sequence/time metadata only and never copies JPEG bytes.

## Host-native shape (`native/LiveCapture.m`; language-neutral sketch)

```swift
// CoreSimulator calls stay private and exception-guarded, using the proven ObjC probe approach.
struct DeviceSessionID: Hashable { let udid: UUID; let epoch: UUID }
struct Presentation { let sourceWidth, sourceHeight, rotation: Int; let generation: UInt64 }
struct PendingSurface { /* retained IOSurface + presentation + next due + last checked */ }
struct EncodedSample { let session: DeviceSessionID; let metadata: SampleMetadata; let jpeg: Data }

final class LiveCaptureHost {
    // Current surface slots only; the serial sampler refreshes source identities.
    // One CIContext, serial render queue, one render in flight, fair 4fps/device scheduler.
    // A separate writer has one latest encoded slot per device, never a FIFO of frames.
    func attach(_ session: DeviceSessionID) throws { fatalError("not implemented") }
    func detach(_ session: DeviceSessionID) { fatalError("not implemented") }
    func shutdown() { fatalError("not implemented") } // idempotent; EOF invokes it
    private func sampleDueDevices() { fatalError("not implemented") }
    private func publishIfCurrent(_ sample: EncodedSample) { fatalError("not implemented") }
}
```

The native adapter retains one persistent source attachment per device and resolves its current surface for each bounded sample. A serial sampler avoids callbacks and per-frame task creation. Detach releases all retained native objects, and stdin EOF invokes the same idempotent cleanup. Surface/presentation generation is checked before publication.

## Swift (`dashboard/AgentPhone.swift`)

```swift
actor LiveConnection {
    static func open(device: String) async throws -> LiveConnection { fatalError("not implemented") }
    func next() async throws -> LiveUpdate? { fatalError("not implemented") }
    nonisolated func close() { /* cancellation closes the Unix socket */ }
}
actor LiveDecoder {
    // One pending encoded frame; one active decode; one awaited MainActor delivery.
    func replacePending(with update: LiveUpdate) async { fatalError("not implemented") }
}
@MainActor final class LiveCardModel: ObservableObject {
    @Published private(set) var image: CGImage?
    @Published private(set) var status: LiveCardStatus
    func apply(_ decoded: DecodedUpdate) { fatalError("not implemented") }
}
```

Use ImageIO to fully decode JPEGs off-main, then publish a `CGImage`; constructing an `NSImage` alone may leave lazy decoding on the rendering thread. Metadata status can supersede a pending decode immediately. A decode result must match the current stream connection/epoch before display. PhoneCard's live model is keyed by stable UDID, so the existing fleet refresh cannot replace it or clear its image during an operation.
