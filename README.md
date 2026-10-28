# agent-phone

Local iOS Simulator control for agents, designed and tested on an 8 GB Apple Silicon Mac. A Go service owns the fleet, SimSlim trims background services, persistent native helpers provide accessibility and input, and CLI/MCP clients share the same per-device ownership rules. A small SwiftUI dashboard shows the fleet and continuous live screen samples.

The demo is the official [Wikipedia iOS app](https://github.com/wikimedia/wikipedia-ios), pinned to release **7.9.3** (`f212471a91279275bdaf77f7fe2c85508a6039ed`). It supports two- and four-phone demonstrations with independent appearance, text size, and search queries. No model credentials or Apple Developer account are required.

## Run

Requires Apple Silicon, full Xcode selected with `xcode-select`, an installed iOS Simulator runtime, `make`, and Python 3. The checked-in demo uses iPhone 17 Pro / iOS 26.5. Use `build/agent-phone catalog` and edit `examples/wikipedia.json` for another installed runtime.

```sh
make setup       # downloads checksum-verified local Go and idb helpers
make app         # builds CLI, guest input helper, host vision helper, and dashboard
make demo        # fetches pinned Wikipedia, builds, runs two variants and smoke test
open 'build/Agent Phone.app'
```

The first build downloads and compiles Wikipedia's dependencies. Later runs reuse its DerivedData. Xcode builds run serially with two compiler jobs; simulator work runs independently per phone. Simulator apps use ad-hoc signing to preserve entitlements such as shared app groups. Wikipedia's newer main branch requires APIs unavailable in the tested SDK, so setup deliberately pins a release.

`make demo` leaves the two managed phones running. Its smoke test searches for **Mars** on the light phone and **Ocean** on the dark phone, asserts the input and result labels, tests ownership conflicts and stale references, samples observation timings, and checks MCP text/image delivery on each phone. Results are saved to `.cache/wikipedia-smoke.json`. Internet connectivity is needed for Wikipedia search.

## Four simultaneous phones

```sh
make app
make demo-four
open 'build/Agent Phone.app'
```

`demo-four` uses `examples/wikipedia-four.json` and keeps four phones running. It reuses the original two managed devices and adds Light with larger text and Dark with standard text. Their queries are **Saturn**, **Volcano**, **Jupiter**, and **Forest**, in manifest order. The regular `make demo` remains the two-phone baseline. See [the four-phone validation](docs/validation-four-2026-10-28.md) for measured memory, failures found and test evidence.

The four-phone target explicitly requests a **6144 MiB** fleet admission budget for a new service. The default remains half physical RAM. If a service is already running with a lower budget, the target stops before building or creating phones and reports that mismatch. Once its leases are released, restart that specific `agent-phone serve` process with `AGENT_PHONE_BUDGET_MIB=6144` and rerun the target. Client environment variables cannot reconfigure a running daemon. The critical memory-pressure boot check is unchanged. Four phones can increase host swapping even when admission succeeds.

The smoke harness reads the chosen manifest, requires exactly one running managed device per variant, enters and verifies a distinct preflight value, and synchronizes all workers before new input. It requires overlapping search-operation intervals across every phone, checks each phone's own query and result, and checks all final states again before releasing the leases. It tests lease conflicts, stale references, and MCP text/image output on every device. A worker failure breaks the barriers, releases acquired leases, writes the partial report, and exits nonzero. Repeated warm observations alternate native-only and screenshot modes, with five samples per mode and both service and client timings.

Evidence is saved to `.cache/wikipedia-four-smoke.json`, including setup results and partial failures. The native dashboard uses smaller previews for three or more phones and a wider initial window. Existing saved windows may need to be enlarged to see all four cards together.

To rerun controls without rebuilding:

```sh
python3 scripts/smoke-wikipedia.py --matrix examples/wikipedia-four.json --queries Saturn Volcano Jupiter Forest --output .cache/wikipedia-four-smoke.json
```

## Agent interface

```sh
build/agent-phone list
build/agent-phone catalog
build/agent-phone call create '{"name":"Feature A","deviceType":"com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro","runtime":"com.apple.CoreSimulator.SimRuntime.iOS-26-5"}'
build/agent-phone call acquire '{"device":"UDID_FROM_CREATE","owner":"my-agent"}'
build/agent-phone call boot '{"device":"UDID_FROM_CREATE","lease":"TOKEN_FROM_ACQUIRE"}'
build/agent-phone call observe '{"device":"UDID_FROM_CREATE","lease":"TOKEN_FROM_ACQUIRE","screenshot":true}'
```

`observe` returns native labels, values, frames, ancestor context where present, supported actions, an observation ID, and element references. Only unambiguous elements whose app, context, and native attributes agree across the tree and target receipt are actionable. Incomplete accessibility trees remain passive. Use the returned IDs for input:

```json
{
  "device": "UDID_FROM_CREATE",
  "lease": "TOKEN_FROM_ACQUIRE",
  "observation": "OBSERVATION_ID",
  "ref": "ELEMENT_REFERENCE",
  "action": "press",
  "screenshot": true
}
```

Send this to `call act`. `type` replaces a text field's value and verifies readback; it accepts a `text` parameter. Other supported actions are `increment`, `decrement`, `scroll-up`, and `scroll-down`, where exposed. Every mutation consumes its observation. Failed target validation discards the helper; observe again to reconnect. Use the returned fresh observation, or observe again during an animation. An input acknowledgment is not a task-success assertion. Observations expire after 30 seconds and a new observation invalidates older references.

Operations on one phone are serialized; different phones can be controlled concurrently. A lease lasts 30 minutes of inactivity, and expired helpers are reaped within 30 seconds. Call `release` with device/lease to close helpers and relinquish control, or `shutdown` first to free simulator memory. Live vision runs independently of these control operations and their leases.

Existing simulators are listed read-only. Only devices created by agent-phone may be acquired, booted, installed, configured, or shut down through its API. The service socket is local and user-only. Leases prevent accidental collisions among cooperating clients; they are not a security boundary against other processes running as the same macOS user.

## UI waits

Use `wait` after input when the app needs time to produce a result. It observes the selected phone without repeating input.

```sh
build/agent-phone call wait '{
  "device":"EXACT_MANAGED_UDID",
  "lease":"LEASE_TOKEN",
  "conditions":[
    {"where":{"role":"textfield","value":"Saturn"},"evidence":"tree"},
    {"where":{"role":"text","label":"Saturn"},"evidence":"tree"}
  ],
  "timeoutMs":10000,
  "screenshot":true
}'
```

`conditions` contains 1–16 positive selectors. Fields within `where` use exact string equality for `role`, `label`, `identifier`, `value`, and `context`. An omitted field is unconstrained; an empty string means equal to empty. `action` selects an existing supported action and requires `native-hit` evidence. The default `mode:"all"` requires every condition in one observation. Use `mode:"any"` for alternatives such as onboarding buttons. Conditions do not accumulate across observations.

Each condition explicitly chooses its evidence:

| Evidence | What a match establishes |
| --- | --- |
| `tree` | An element is present in the normalized accessibility tree. |
| `native-hit` | Its app, context, and semantic identity also match a native accessibility receipt at its center. `nativeHitAt` records receipt completion. |

**Neither mode proves visual visibility.** In the test fixture, a fully covered label appears in both the tree and the native center hit. `visible` is not a supported evidence value. A passive label with a native hit remains non-actionable. Native receipts and screenshots are separate captures; screenshots have their own `screenshotAt` timestamp.

`timeoutMs` defaults to 30000 and accepts 1–30000. The budget starts at service entry and includes phone-lock admission, helper startup and I/O, polling, and the requested screenshot. A matching result completed after the deadline is rejected. OS cleanup and response delivery can finish later, so this is an acceptance deadline rather than a hard real-time response guarantee. A successful wait returns `observation`, `satisfied` condition indexes, `attempts`, and `elapsedMs`. It replaces previous action references.

A timeout exits nonzero with `error.code:"wait_timeout"` and partial diagnostics. `lastObservation`, when present, is the last complete in-budget sample and is non-actionable. Cancellation returns `cancelled`. Invalid requests, lost leases, and helper failures stop the wait immediately. The loop retries nonmatching observations and two explicit accessibility readiness responses, `kAXErrorServerNotFound` and a missing frontmost application. Other reader errors are terminal. One phone stays serialized during its wait; other phones and live vision operate independently.

## Live vision

Read the current screen without a control lease:

```sh
build/agent-phone call frame '{"device":"EXACT_MANAGED_UDID"}'
build/agent-phone watch --device EXACT_MANAGED_UDID
```

`frame` starts capture if needed and waits at most eight seconds for a fresh frame. A successful result has `state:"live"` and a `frame` object containing the exact managed `device`, `cursor:{epoch,sequence}`, `capturedAt`, `width`, `height`, and base64 `jpeg`. The source records `capturedAt` before IOSurface readback. It is independent of accessibility timestamps.

Wait for a later sample using the previous result's cursor:

```sh
build/agent-phone call wait_frame '{"device":"EXACT_MANAGED_UDID","afterEpoch":"EPOCH_FROM_FRAME","afterSequence":1,"timeoutMs":1000}'
```

`timeoutMs` accepts 1 through 30000 and defaults to 20000. A newer epoch also satisfies the wait after a worker or service restart. A timeout returns `timedOut:true` and the source state without an image. Repeated pixels still count as a new sampled frame. Use accessibility and task-specific assertions to determine whether an action completed.

Both RPCs and the dashboard share one persistent `AgentPhoneVision` process per subscribed phone. Four devices and eight readers per device are allowed at once. Each stream keeps only its newest frame. Slow readers skip older captures. The last reader starts a two-second idle grace period so repeated agent calls can reuse capture. Shutdown, boot, failed capture, and service exit retire the worker's epoch. A replacement waits until the previous worker has exited.

`watch --device UDID` opens one local Unix-socket HTTP connection and writes bounded NDJSON records to stdout. The underlying route is `GET /vision/stream?device=UDID`. Records contain `device`, `state`, an optional `frame`, and an optional diagnostic `error`. States are `starting`, `live`, `reconnecting`, `stale`, and `offline`. Status records may retain the last frame with its original cursor and capture time. An image in a non-live status is historical. The stream closes on canceled requests or blocked writes after three seconds. `watch` exits on broken stdout or termination signals.

The host source samples the primary class-0 IOSurface at most four times per second, encodes at 402 pixels wide with JPEG quality 0.68, and uses no per-frame files or processes. JPEG payloads are limited to 2 MiB and metadata to 8 KiB. Only Booted, explicitly managed phones are eligible. A missing surface or stale source cannot become a new live image. The helper uses `DEVELOPER_DIR`, or resolves `xcode-select -p` once at startup.

Dashboard cards each keep one watch process while visible and Booted. Image decoding happens off the main thread, and the UI receives only the newest pending update. Cards retain the last image and show its capture age and Live, Reconnecting, Stale, or Offline state. Fleet metadata still polls separately every five seconds. Changing keyboard focus does not pause capture. Hidden-window pausing is not implemented.

Live vision never acquires, extends, expires, or releases a control lease. It never replaces accessibility observations or element references. The existing `observe` screenshot option still saves evidence files.

See [the live-vision validation](docs/validation-live-vision-2026-10-28.md) for four concurrent searches, frame timing and resource samples, helper recovery, slow-reader handling, reboot, and idle cleanup. To repeat the streaming/control check on the four running demo phones:

```sh
python3 scripts/verify-live-vision.py --seconds 60 --output .cache/live-vision-validation/four-live.json
```

## MCP

Run `build/agent-phone mcp` as a stdio MCP server. Example client configuration, with the absolute path adjusted to your checkout:

```json
{
  "mcpServers": {
    "agent-phone": {
      "command": "/Users/gp/src/agent-phone/build/agent-phone",
      "args": ["mcp"]
    }
  }
}
```

The server exposes 17 `phone_*` tools for fleet, catalog, creation, ownership, lifecycle, builds, configuration, observation, actions, and matrices. Screenshots are returned as MCP image content alongside the accessibility JSON. `phone_frame` and `phone_wait_frame` return vision metadata and one JPEG image block. Their text blocks omit the base64 image payload. No model is built into the service. Adding the server to an agent client is a separate client configuration step; building this repository does not alter client settings.

A persistent MCP process shares a bounded pool of at most eight service connections and closes idle connections when it exits. Failed matrices and UI waits return `isError:true` with their partial report in the text payload. `phone_wait` uses the same conditions as `call wait` and attaches a requested screenshot on success. MCP cancellation notifications cancel the corresponding request; EOF cancels outstanding requests. At most eight requests are pending, and overload is rejected without blocking cancellation.

## Projects and feature variations

`call build` takes an absolute `.xcodeproj` or `.xcworkspace` path, scheme, optional configuration, and `buildSettings`. Default DerivedData directories are keyed by project, scheme, configuration, and settings. A caller may explicitly supply an absolute `derivedData` path to reuse an existing build. Each completed build returns an isolated app snapshot and log under the service's `build-artifacts` directory; later builds cannot overwrite those returned paths. A host-wide lock serializes agent-phone Xcode builds across service homes through snapshot publication. Build settings that redirect output directories are rejected.

Path normalization follows the macOS volume's case sensitivity and resolves symlink aliases. On case-insensitive volumes, create any non-ASCII output directories before using them; new directory components must be ASCII so output comparisons can be exact. Existing Unicode paths are supported.

`call matrix` takes an absolute path to a JSON manifest. See `examples/wikipedia.json`. Each variant can specify appearance, Dynamic Type size, launch arguments, steps, and a complete `build` override for another project or scheme. Project paths in a manifest are relative to that manifest. Steps support exact `label` or `identifier` targeting; ambiguous matches fail. `expect` checks an exact accessibility-tree label or string value in the current observation. It does not establish visual visibility. Use an explicit `wait` step for asynchronous readiness; it accepts the same `conditions`, `mode`, `timeoutMs`, and `screenshot` fields as `call wait`. A successful wait counts as an assertion. Startup uses the same bounded observation loop to obtain its first complete sample, with a 30-second deadline.

Before building, a matrix rejects different build specifications that claim the same or nested DerivedData directories. Reused devices must have exactly one registered name and match the live simulator's name, runtime, device type, and availability. A mismatch fails without acquiring or changing that phone. Lookup, creation, and lease acquisition are serialized across matrix runs.

Matrix reports contain aggregate `status`, per-status `counts`, and every variant result. Failed or skipped variants make the CLI exit nonzero while retaining `result` alongside `error` in its JSON envelope. Build failures retain their log and dependent rows. The smoke harness also saves a failed setup report under `matrixRun`.

With `keepRunning:false`, each completed variant shuts down its phone and releases its lease. Capacity-blocked workers wait up to two minutes. With `keepRunning:true`, capacity shortages are reported immediately so waiting cannot deadlock on retained phones. Worker concurrency is 1–4; the default is two. Builds always remain serial. This is a bounded local runner, not a persistent job scheduler.

## Memory and evidence

The boot budget defaults to half physical RAM (4 GiB on the baseline Mac). Admission counts measured memory of **all** running simulators, reserves 1200 MiB for a new phone, and rejects new boots during critical macOS memory pressure. This is a conservative admission estimate, not a hard runtime memory limit: apps can grow after launch. The default profile keeps `apsd` for apps that register for notifications during startup; `boot` accepts additional SimSlim `keep` labels and `except` categories.

State lives in `~/Library/Application Support/AgentPhone`: the managed-device registry, event log, screenshot artifacts, build metadata/logs, and per-matrix reports. Leases are in memory and are invalidated when the service restarts. Live frames stay in bounded memory. They are not saved as evidence. The dashboard retains its last image with a reconnecting or stale label until fresh capture resumes; persisted screenshots and reports remain. Evidence has no automatic retention policy yet.

Environment overrides are `AGENT_PHONE_HOME`, `AGENT_PHONE_ROOT`, and `AGENT_PHONE_BUDGET_MIB`. Set them before starting the service. `serve` runs in the foreground for debugging; ordinary CLI commands start the service automatically. After rebuilding a running service, stop that specific `agent-phone serve` process and let the next call start the new binary. Do not terminate Simulator or unrelated user processes.

## Current boundaries

- Tested: two- and four-phone Wikipedia runs on the same iOS 26.5 runtime, different system appearance/text size and independent searches. Multiple distinct Xcode projects are supported by the build interface but have not been demonstrated in the live test.
- Native accessibility only. Web content, remote elements, and controls that cannot be matched to a native identity remain observable but cannot be clicked through this first backend. A native identity match does not prove a control is visually unobscured. There is no arbitrary coordinate-tap fallback or OCR. Live vision is sampled JPEG output at up to four frames per second.
- Native helper APIs are private Apple interfaces. Compatibility must be checked for each Xcode/runtime update. If an accessibility helper goes idle for two minutes, a subsequent read may fail and discard it; observe again to reconnect. Vision workers restart with bounded backoff after failure or a three-second source stall. Inputs are never automatically retried.
- Text replacement can take effect after the native helper's immediate readback reports failure. Inspect a fresh observation before deciding on another action. This occurred in the first four-phone P1 validation run; the subsequent run with different queries passed.
- Accessibility and screenshot timestamps are separate; their capture is not atomic. A post-action observation can still show an animation or loading screen.
- The accessibility tree and native target receipts are separate captures without a shared native identity. Matching attributes and context cannot detect every semantically identical replacement between those captures.
- The dashboard is a development bundle pointing at the CLI in this checkout. Rebuild it after moving the checkout. Distribution packaging, physical phones, vphone virtual machines, richer scheduling and automatic artifact cleanup are future work.

## Development

```sh
make test                          # Go race detector and behavior tests
make build                         # compile CLI, guest input helper, and host vision helper
make vision                        # compile only the macOS host vision helper
python3 scripts/smoke-wikipedia.py  # live checks on the already-running demo phones
python3 scripts/verify-ui-waits.py --device EXACT_MANAGED_UDID --shutdown
```

The UI wait check builds a local fixture, runs its matrix on the selected unowned managed phone, and then searches Wikipedia. Wikipedia must already be installed. Use a phone named `Matrix / Variant`; the check preserves its registration and releases its lease. It restores an initially shut-down phone, or shuts it down when requested. Reports include partial failures and live observation artifacts.

See `THIRD_PARTY.md` for exact upstream revisions and licenses. SimSlim and the Jev fork are reused without importing Jev's inference runtime or vphone's firmware pipeline. This repository does not modify either source checkout.

See [the P1 validation](docs/validation-p1-2026-10-28.md) for regression evidence, live build isolation, matrix failure handling, and four-phone results.

See [the UI wait validation](docs/validation-ui-waits-2026-10-28.md) for deadline checks, the covered-label counterexample, and CLI/MCP/matrix results.
