# Validation — 2026-10-27

Executed on Mac14,2, 8 GiB RAM, 8 CPUs, macOS 26.3 (25D125), Xcode 26.6 (17F113), iOS 26.5 Simulator. Wikipedia source is the unpatched release 7.9.3, revision `f212471a91279275bdaf77f7fe2c85508a6039ed`.

`make test` passed all eight behavior tests with Go's race detector: exclusive concurrent acquisition, wrong/expired leases, unmanaged-device protection, memory admission arithmetic, build-output isolation, target identity independent of unrelated labels, blank text fields, and offscreen exclusion. CLI, Objective-C helper, and SwiftUI dashboard builds succeeded.

`make demo` passed end to end: pinned source setup, serialized simulator build with ad-hoc signing, two managed phones with SimSlim profiles, installation, launch, configuration, observation, and the parallel smoke test. The demo preserves `apsd` and removes 167 background services on this runtime. No changes were made to Wikipedia's source to make it launch; preserving its app-group entitlements fixed the unsigned-build startup crash.

Both variants asserted their own search-field value and matching result label. A competing client was denied a lease; a superseded observation was rejected before input. MCP initialization, 14 tool definitions, and observation with both JSON text and JPEG image content passed. An additional live check moved the light phone to Settings after capture: the original target was rejected as changed/unavailable, then Wikipedia was restored. The dashboard was visually checked with both search previews.

| Phone | Query | Native observation median | Observation + JPEG median |
| --- | --- | ---: | ---: |
| Wikipedia / Light | Mars | 111 ms | 259 ms |
| Wikipedia / Dark · Large text | Ocean | 114 ms | 296 ms |

These are three warm samples per mode per device from the last smoke run, while both device workflows ran concurrently. They measure the service's observation duration, excluding model latency and client transport. Cold helper startup and the first keyboard transition were slower, roughly 1–3.6 seconds in earlier checks. This is a small functional sample, not a sustained-load benchmark.

The final smoke report measured **2.92 GiB** across the two running simulators, against a **4 GiB** boot budget. The value includes the apps and their simulator processes, not total macOS memory usage. Both phones remain running and their leases are released.

Evidence stays local:

- `.cache/demo-final.log`: complete final demo invocation.
- `.cache/wikipedia-smoke.json`: assertions, timings, screenshots, and fleet readings; completed 2026-10-27T21:08:43-0700.
- `.cache/native-target-smoke.json`: rejection after the foreground-app switch.
- `.cache/final-checks.log`: race tests and CLI/native compilation.
- `~/Library/Application Support/AgentPhone/runs/`: per-variant matrix reports.
- `~/Library/Application Support/AgentPhone/artifacts/`: captured screenshots.

Limits: one app project, two runtime-configuration variants, one runtime and one host were live-tested. More phones, distinct Xcode projects, long sessions, memory-pressure recovery, and alternate runtime versions have not been validated. Native web/remote controls are observation-only in this backend. The memory gate is admission control, not an enforceable RAM cap.
