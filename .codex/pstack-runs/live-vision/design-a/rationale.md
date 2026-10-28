# Rationale

## Problem

Agent Phone's AX observation is a leased, consumable action capability, while its optional screenshot is permanent evidence. Reusing that path for live vision would invalidate action references, contend on `Phone.mu`, fork a capture command per request, and make a dashboard view disappear whenever `fleet` cannot take the operation lock. Four simulators on an 8 GiB Mac require shared capture, bounded demand, and measured resource cost. Grounding was checked directly in `service.go` (`Call`, `Close`, `boot`, `fleet`), `observation.go` (`observe`, `act`), `transport.go`, the Unix HTTP/MCP implementation, the full SwiftUI dashboard, and the pinned native framebuffer research.

## Usage (caller's view)

Open the dashboard; ask `phone_frame(device)` for its shared current image; pass the returned `after` cursor to wait for a change. No lease is needed for any of these. Use the existing leased `observe`/`act` calls for native verified actions. The complete usage and three call sites precede the type sketch in `usage-and-lifecycle.md`.

## Shape

Choose one demand-driven macOS helper with one Core Image pipeline and per-device persistent IOSurface attachments, feeding a latest-image Go broker. The broker hides source health, lifecycle, restart, freshness, epochs, wait semantics, and fan-out behind a frame read and reader handle, per interface-depth and boundary-discipline. Private protocol details never enter domain signatures. A single Go owner loop writes feed state; the native sampler owns per-device source slots and coalesces due ticks, per separate-before-serializing-shared-state. Immutable image objects and private epochs keep old workers from reviving retired captures, per encode-lessons-in-structure. The managed registry remains the authority; active feeds are not a second registry, per single-source-of-truth. Source-to-user tracing needs the native helper, `live.go`, and the selected adapter, rather than a chain of framework layers.

## Synthesis decision

Candidate A recommends the JPEG broker as its base. Arena synthesis is not performed by this runner. Retain the alternative comparison below when choosing between candidates; do not silently turn this package into two capture backends.

## Tradeoffs accepted

- We accept bounded lossy sampling at 402-pixel portrait width/4 fps in exchange for one usable image representation shared by dashboard, CLI, and MCP. Explicit full-resolution evidence remains available separately.
- We accept JPEG encode/decode work in exchange for independent frame delivery: readers can join anywhere, skip any number of frames, and immediately render the next image.
- We accept one helper crash affecting all four feeds in exchange for one CIContext and one bounded encoder. Epochs and supervision restore feeds independently of control.
- We accept a short warm-idle grace period and bounded source-health sampling in exchange for cheap repeated agent reads and reliable static-screen status.

## Alternatives considered

1. **Direct native display sharing.** A macOS helper exports IOSurface Mach send rights over XPC; SwiftUI presents them with Metal, and Go requests JPEG conversion only for agent reads. This hides display efficiency behind a native viewer API, but exposes an additional lifetime and security model to the viewer, requires a second agent image route, and allows readers to observe a changing shared buffer without a stable common frame identity. It wins for high-FPS local presentation, loses for this small Go service's common dashboard/agent contract and cleanup complexity. Raw global IOSurface IDs are not a substitute for rights/lifetime management.
2. **Shared H.264 stream.** A persistent VideoToolbox encoder per device sends compressed video to dashboard decoders; a Go/native image endpoint retains or decodes the latest image for MCP. It hides compression efficiency but exposes keyframe joining, decoder state, format changes, and discontinuity recovery. Slow readers cannot freely skip arbitrary compressed frames; they need a keyframe recovery policy. A second still-image contract remains necessary. It is a stronger candidate if measurements require smooth high-FPS viewing, not for initial 4 fps agent eyes.
3. **One JPEG capture helper per device.** Same clean caller interface and stronger failure isolation, but four CIContexts, native runtime footprints, and independent encoding bursts are a worse initial fit for the memory-constrained host. This is a real process-ownership alternative, not an additional supported backend.

## Open questions and risks

- The parent-owned persistent attachment/sampling gate passed on all four phones (`../probe-results.md`); does the integrated version also handle rotation, disappearance, and EOF cleanup correctly?
- Do four live feeds meet the stated memory/latency targets without memory compression growth, and what global cadence reduction is needed if they do not?
- Can a source render race during Core Image readback produce visible tearing on this runtime, and does the native API provide a completion/fence primitive that must be added if the prototype detects it?

These are prototype/measurement gates, not reasons to ask the user to choose unvalidated private APIs.

## Red-flag screen

No public wire types, parallel capture API, action-reference coupling, per-reader capture, unbounded event queue, or hand-synchronized managed-device list. There is one capture policy, one owner for Go feed state, and one cleanup path. The caller needs only current/change reads or a closeable reader; neither caller coordinates native attachment stages.

## Next implementation step

Using the now-passed isolated native prototype, implement `live.go` against a bounded fake worker and lock down lifecycle/epoch behavior before integrating the real host helper and dashboard.

Design status: **PASS**. The parent reports the native gate passed; sustained integrated four-stream resource validation remains required; no production files, builds, or simulator state were changed by this runner.
