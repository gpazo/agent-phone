# Native capture gate

Passed on 2026-10-28 using all four managed iOS 26.5 devices and Xcode 26.6. The host Objective-C prototype attaches once to CoreSimulator and samples the primary IOSurface without launching per-frame processes. It emitted readable 402 × 874 JPEGs on every phone. Parent inspected the first image and a changed Wikipedia query image.

Four parallel probes emitted 48–51 frames each over approximately seven seconds at an 8 Hz configured upper bound (actual cadence lower because encode work is serial). JPEGs were roughly 61–74 KB. One leased native type action changed Wikipedia's query to Mercury while all four feeds continued. Its AX result and captured image both confirmed Mercury. Exact output: `.cache/live-vision-probe/four/results.json` and images in that directory. The probes were terminated and leases released.

A single `ps` sample showed each probe at 2.4–3.0% CPU and about 17 MB RSS. This is an exploratory sample, not a sustained benchmark or total memory footprint. Production starts conservatively at at most 4 Hz, 402-pixel width, JPEG quality 0.68; verify the final integrated stack separately. No performance speedup claim is justified by this probe.

Runtime fixes required UUID normalization for NSUUID/string map keys and `SimDevice.UDID`, and exception-guarded calls instead of relying on proxy `respondsToSelector:`. One non-display port reports an unsupported displayClass selector; the primary class-0 display is found correctly. Production must reject ambiguous display selection, check Booted state, put real source wall-clock timestamps before capture, cap payloads, retire stale workers, and bound shutdown.
