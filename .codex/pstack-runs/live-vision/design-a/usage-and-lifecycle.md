# Candidate A: one native sampler, shared latest JPEGs

## Usage first

The dashboard subscribes once per visible managed phone. Opening a second dashboard or asking the agent for a frame joins the same capture. No lease is acquired, renewed, expired, or released by vision reads.

```sh
# Current shared frame; starts the persistent sampler if cold.
agent-phone call frame '{"device":"EXACT_MANAGED_UDID"}'

# Wait for a changed sampled image, a reset, or a terminal/status change.
agent-phone call frame '{"device":"EXACT_MANAGED_UDID","after":"OPAQUE_CURSOR","waitMs":10000}'

# Actionable controls remain a separate native AX observation.
agent-phone call observe '{"device":"EXACT_MANAGED_UDID","lease":"TOKEN","screenshot":false}'
```

`phone_frame` exposes the same parameters through MCP. A successful image response contains compact text metadata plus one JPEG image content block; it does not duplicate image base64 in the text. A timeout/status response contains text only. MCP never pushes unsolicited frames. CLI JSON includes metadata and `image:{mimeType:"image/jpeg",data:"BASE64"}`. Existing explicitly requested evidence screenshots remain retained full-resolution files with their existing timestamps.

The frame response is `{device, state, reason, cursor, frame?, checkedAt?, error?}`. `frame` carries `{epoch, sequence, sampledAt, receivedAt, width, height, sourceWidth, sourceHeight, rotation, image}`. States are `starting`, `live`, `idle`, `disconnected`, `stale`, `unsupported`, and `closed`; reasons are `current`, `changed`, `reset`, `status`, and `timeout`. A frame is visual evidence, never an actionable observation ID or element reference.

Three caller sketches:

```go
// /rpc frame dispatch, AFTER exact registry lookup, BEFORE p.mu/lease handling.
return s.live.read(ctx, managedPhone, query) // no AX/control state access

// Dedicated HTTP stream handler over the existing 0600 Unix socket.
reader, err := service.WatchLive(r.Context(), exactDevice)
if err != nil { /* ordinary API error before starting the stream */ }
defer reader.Close()
for {
    update, err := reader.Next(r.Context()) // initial state, then newest update
    if err != nil { break }
    if err := writeVisionRecordWithDeadline(w, update); err != nil { break }
}
```

```swift
// One task per visible phone; all images decoded away from the main actor.
let reader = try await LiveConnection.open(device: phone.udid)
defer { reader.close() }
while let update = try await reader.next() {
    await decoder.replacePending(with: update) // one pending, one decoding
}
// Decoder awaits one MainActor publication before accepting another result.
// It rejects results superseded by a newer epoch or non-live status.
```

`frame` without `after` returns the current verified image immediately when warm, or waits up to 2 seconds for the first image when cold. `waitMs` is 0–20,000, defaulting to 2,000 without `after` and 10,000 with it. Zero is a nonblocking cache/status read and does not start or keep capture alive. A nonzero call holds temporary reader interest only until its response/cancellation. `after` is an opaque cursor bound to device, capture epoch, image sequence, and status revision; callers cannot construct ordering from it. Waits ignore ordinary health heartbeats and wake for a different image, epoch, or externally visible state. They return `timeout` with no repeated image when unchanged. A cursor from another device is rejected; a recognized old epoch returns `reset`, never waits indefinitely.

## Primary shape and module map

One persistent **macOS** native helper captures all currently demanded managed iOS simulators. It attaches once to each main-display IOSurface, renders a small image through one shared CIContext, and sends changed JPEGs to Go. A separate Go live manager owns demand, epochs, latest images, and worker supervision. Neither the helper nor the manager uses `Bridge`, `observe`, `capture-targets`, `observation`, or `lastObservation`.

| File | Ownership/change |
| --- | --- |
| `internal/phone/live.go` (new) | Domain frames, one manager loop, reader interest, latest-state slots, cursors, lifecycle, worker supervision. Private native record parsing can remain here; split only if actual size warrants it. |
| `internal/phone/service.go` | Own manager; route `frame` after registry lookup and before `p.mu`; expose `WatchLive`; notify manager around boot/shutdown; close it on service exit. Fleet reads only small live metadata independently of `p.mu`. |
| `native/LiveCapture.m` (new; reuse the validated host-native approach) | macOS CoreSimulator attachment, runtime-guarded private calls, retained IOSurfaces, bounded sampler, JPEG encoding and framed IPC. No idb framework dependency. |
| `cmd/agent-phone/main.go` | `frame` RPC/CLI/MCP adaptation; `/vision` subscription route; binary stream serialization; frame-specific request/response bounds and cancellation. |
| `dashboard/AgentPhone.swift` | Live Unix-socket reader and decode actor; cards show streamed images and state. Existing five-second fleet refresh remains metadata only. |
| `Makefile`, `scripts/build-app.sh`, docs | Separate macOS host-helper build/bundle path; keep guest `AgentPhoneActions` target unchanged; record any adopted reference code and pinned revision in `THIRD_PARTY.md`. |

No new package framework, provider abstraction, HTTP server, external dependency, or durable image store is needed. Existing `Frame` means AX geometry, so the new image type is `LiveImage`.

## Native capture and change semantics

The worker starts once on first demand and resolves exact managed UDIDs passed by Go, normalizing NSUUID/string identities at the native boundary. Its adapter loads the selected Xcode's CoreSimulator libraries, locates the iOS main display (`displayClass == 0`), and refuses an ambiguous/non-iOS source. Private remote calls use an Objective-C exception guard; successful selector lookup alone is not proof that a remote proxy implements a call.

Use the validated persistent sampling path first: attach once, retain the current primary IOSurface, and sample it at no more than 4 Hz per demanded device. One serial render queue selects due devices round-robin, with one render/encode in flight globally and coalesced missed ticks. Re-resolve the current surface and presentation metadata through the persistent native attachment on each sample, then retire stale surface references. This is an in-process IOSurface read, never `simctl` screenshot polling. There is no callback dependency, per-frame process, or queued sample backlog. Native callbacks are a future optimization only if measured idle cost justifies them; do not implement both paths initially.

Render directly from IOSurface through Core Image to sRGB pixels with a 402-pixel portrait width (swap output dimensions for landscape), with a hard 1,024-pixel long-edge cap, preserving aspect ratio and display orientation; do not first materialize a full-size CGImage. Hash these downsampled pixels and presentation metadata. Encode JPEG quality 0.68 only when that hash changes or this attachment has no published frame. Thus image sequence means a changed **sampled image**, not an AX mutation or source presentation count. Changes entirely between samples or below the chosen resolution are intentionally unobservable. No knob per reader alters shared capture policy.

Before publishing, ensure the retained surface/configuration generation still matches; discard superseded work. Retain the source while rendering, release replaced surfaces deterministically, and use an autorelease pool per sample. Disable CI intermediate caching and clear retained GPU caches when the last attachment ends. A surface becoming nil immediately invalidates freshness. Device boot state and source attachment health are verified by the native adapter each second using CoreSimulator, not by a Go `simctl` loop. Health reports must follow a successful sample of the current surface; a process-alive heartbeat alone cannot make an image live.

## Frame identity and freshness

Go issues a random capture epoch for every attach/re-attach and worker restart. Commands and records include it. Go accepts records only for the currently active device/epoch, then assigns monotonically increasing image sequence and a separate externally visible status revision. A record from a retired epoch is discarded before publication, even if it arrives after a new worker is ready.

`sampledAt` is the native sampling instant, not display presentation time; `receivedAt` is Go receipt time. Source and output dimensions plus applied rotation are explicit. Native elapsed-time fields and Go monotonic receipt times drive deadlines/age; wall-clock timestamps are presentation only. AX `capturedAt` and evidence `screenshotAt` remain separate and have no atomic relationship to this frame.

Static screens can keep the same sequence indefinitely and remain `live` while current-surface checks succeed. Three seconds without a valid check means `stale`; a known source loss means `disconnected` immediately. The dashboard labels both and preserves its last decoded image with the last sample time. Go clears the image on retirement, so the old image can never re-enter a new live epoch. A healthy new epoch becomes live only after its first valid frame. `checkedAt` describes source verification, never a fabricated new capture time.

## Lifecycle and backpressure rules

| Trigger | Required behavior |
| --- | --- |
| First stream or bounded frame wait | Create one feed, issue a new epoch, start helper if absent, attach. Other simultaneous readers join it. Never take `Phone.mu`. |
| Reader stop/disconnect/cancel | Release only that reader, idempotently. HTTP request cancellation propagates to its pending wait. No global stop command that one reader can use to disrupt others. |
| Last reader leaves | Keep attachment warm for 3 seconds; new interest cancels this timer. Then retire epoch, clear image, detach, and publish `idle`. With no feeds, terminate/reap helper. Repeated one-shot calls can reuse this grace period. |
| Lease acquire/release/expiry, observe, act, install, launch, configure | No live lifecycle effect. Existing native AX/action-reference behavior remains intact. App transitions arrive through the display feed. |
| Managed shutdown or boot/reboot | Before the existing operation begins, retire affected epoch and suspend attachment; do not wait for worker I/O while holding `Phone.mu`. Resume source discovery only after the operation returns and demand exists. If shutdown fails, rediscover instead of reviving an old epoch. |
| External simulator shutdown/reboot | Native state/source checks retire that device epoch; retry while interested. A down device produces status, never cached live bytes. Return requires new attachment and first frame. |
| Worker crash, malformed IPC, output stall | Retire all its active epochs; publish disconnected; close pipes, reap process, retry with 250 ms/1 s/4 s/10 s capped backoff while demand remains. Ten-second healthy operation resets backoff. Unsupported capabilities produce `unsupported` without a hot restart loop. |
| Service close | Once-only close: reject new readers, cancel waits, retire all epochs, close stdin, bounded graceful wait then kill/reap helper, close pipes. Native stdin EOF also releases surfaces and exits, covering parent crashes. |

Shutdown notifications must be an acknowledged, quick manager transition before the control operation starts; they schedule detach without joining a blocked process. The manager never calls back into `Service` or acquires a control mutex, preventing lock cycles. Existing calls can then complete without capture ownership races.

The Go manager is the sole writer of feed state. It owns a maximum of four attached feeds, a single latest immutable JPEG per feed, close-and-replace change-notification channels, and reader counts. The native reader drains into a bounded event channel; if transport backs up, the helper's one-slot-per-device writer mailbox overwrites pending frames. Its writer selects devices round-robin and enforces a 1-second write deadline; blocked output terminates the worker. No callback, decoder, sender, or reader keeps an unbounded frame queue.

Set 1 MiB maximum JPEG payload, 4 KiB record header, 16 simultaneous readers/waits total, and 8 per phone. Limits fail explicitly with retryable capacity errors. Each stream sender holds at most one image while writing, then requests the newest snapshot; slow readers skip sequence numbers. Set a 1-second deadline per HTTP record write, not a global streaming-response deadline. A client that cannot keep up is disconnected and may reconnect. These bounds include RPC/MCP frame serialization; do not let generic unbounded MCP goroutine creation bypass live reader admission. Native oversize/malformed frames are rejected before Go allocation/publication and surface an error; native rendering itself is bounded by the output pixel dimensions. Runtime/Core Image allocations still require measurement.

Both worker IPC and `/vision` use private versioned binary records: two big-endian uint32 lengths, JSON metadata, then optional raw JPEG. Maximum lengths are validated before allocation. Native stdin commands are small bounded NDJSON. Metadata/status records have zero image bytes. No image goes to `events.jsonl`; lifecycle failures and periodic aggregate counters suffice. The `/vision` endpoint uses the existing private Unix socket and validates exact managed device identity through the same service registry. The dashboard's off-main decoder retains one pending frame and one image being decoded, awaits one main-actor publication, and discards superseded epoch/status results. A service EOF immediately overlays “Disconnected” and reconnects with capped backoff; it never leaves a green frozen card.

## Verification gates for implementation

This runner performed no builds or simulator operations. The parent-owned native gate has now passed; see `../probe-results.md`. It demonstrated four persistent 402 × 874 feeds, changed Wikipedia content during AX input, and roughly 61–74 KB JPEGs. Its single ~17 MB RSS / 2.4–3.0% CPU sample per probe is exploratory, not a sustained integrated benchmark.

1. Integrate the now-passed persistent native attachment/sampling gate. Still validate rotation, source disappearance, surface replacement, and EOF cleanup in production; callback validation is unnecessary for the chosen timer path. Do not silently substitute per-frame `simctl`.
2. Fake-worker Go tests cover unleased reads without observation invalidation, shared producer/start coalescing, change waits and timeouts, slow-reader skipping, admission/size bounds, late epoch rejection, idle grace, source retirement, crash restart, and idempotent close. Assert no control lease timestamp/native target identity changes from vision calls.
3. Real four-phone run: dashboard plus concurrent agent reads; make independent UI changes on every phone and verify advancing image content/sequence, not just metadata. Check that static frames do not advance yet remain healthy. Exercise busy AX operation, release, reader disconnect, worker kill, simulator shutdown/reboot, and service exit. Count lingering helper processes, source attachments, open descriptors, and evidence files afterward.
4. Measure baseline and streaming private footprint for helper/daemon/dashboard, simulator totals, compression/swap, CPU, encoding latency, first-frame and change-to-display latency over at least 10 minutes. Record actual results for the 8 GiB Mac. Initial target: four changing feeds at up to 4 fps each, p95 visible-update latency under 500 ms while warm, under 200 MiB added private footprint, no increasing queue/memory trend. These are acceptance targets, not claims; the 6 GiB boot admission budget does not guarantee headroom. If they fail, measure a lower global cadence/resolution before revisiting architecture.
