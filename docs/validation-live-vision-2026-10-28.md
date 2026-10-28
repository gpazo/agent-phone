# Four-phone live vision validation — 2026-10-28

The native dashboard now continuously displays four managed Simulator screens. Agents can read the same capture cache through `frame`, `wait_frame`, and MCP image content without acquiring a control lease or invalidating accessibility references. `watch --device UDID` exposes the persistent NDJSON stream.

## Environment and scope

Apple M2, Mac14,2, 8 GiB RAM; Xcode 26.6 (17F113), iOS 26.5; Wikipedia 7.9.3. Four iPhone 17 Pro simulators used the existing light/dark and standard/large-text demo variants. The daemon retained its explicit 6144 MiB fleet admission budget. No unmanaged phone was changed.

Capture uses one persistent host helper per subscribed phone, directly reading the primary IOSurface. The policy caps sampling at 4 Hz and JPEG output at 402 pixels wide, quality 0.68. Each device keeps one latest frame, with at most eight readers and four active devices. Idle capture stops after a two-second grace period. Native frames carry source timestamps; agent cursors combine an epoch and sequence. Repeated pixels still count as a new sample.

This is sampled live vision. The MCP tools return requested images; they do not push unsolicited video into an agent conversation. Accessibility remains the control and assertion interface, with separate timestamps.

## Checks that passed

- `make test`: Go race tests and five Python tests. `make app`: CLI, iOS action helper, macOS vision helper, and signed SwiftUI development bundle.
- Four concurrent Wikipedia searches for Saturn, Volcano, Jupiter, and Forest. Native query and result labels matched on every device, and all four final JPEGs were visually inspected. Search operation intervals overlapped by 1071 ms.
- Unleased `frame` and `wait_frame` returned fresh, correctly identified images on all four devices. Action references remained valid across those reads. Competing leases and stale action references were rejected. Capture continued after control leases were released.
- MCP advertised 16 tools. `phone_frame` returned metadata plus one JPEG image block without duplicating base64 in its text block.
- Dashboard and verification clients shared exactly four capture helpers. All feeds remained fresh through a 60-second sustained window after the searches; the full run lasted 85 seconds.
- Killing one capture helper recovered its feed in 1188 ms with a new epoch. Suspending one helper triggered stale detection, forced retirement, and recovery in 4708 ms. The other three streams kept their epochs and advanced throughout both checks.
- A non-reading Unix HTTP client was disconnected by the write deadline; only 8192 bytes remained buffered in that check. Healthy readers continued. A CLI whose stdout was blocked exited 4 ms after termination was requested.
- One explicitly leased phone was shut down and booted again. Its old stream closed, an offline wait returned no image, and the rebooted phone produced a new epoch. Wikipedia was relaunched and its Forest query restored.
- Restarting the daemon reaped all four old helpers. The still-open dashboard automatically connected four new helpers with fresh epochs in 1272 ms. The six-GiB budget was preserved.
- Closing the dashboard after other readers had exited left zero capture helpers. Reopening it restored four live cards. Live streaming did not add or modify persisted screenshot evidence.

## Measured sample

Values below describe one functional run, not a before/after benchmark or a long-duration soak. Per-frame summaries retain the last 256 samples per device. Capture age is measured when the verification client receives a frame; it is not action-to-screen latency.

| Variant | Frames in full run | Median interval | Median capture age | p95 capture age | Median JPEG |
|---|---:|---:|---:|---:|---:|
| Light / Saturn | 312 | 265.76 ms | 40.32 ms | 47.08 ms | 61,880 bytes |
| Dark, large text / Volcano | 312 | 265.48 ms | 40.57 ms | 47.14 ms | 68,671 bytes |
| Light, large text / Jupiter | 313 | 265.85 ms | 40.50 ms | 47.75 ms | 69,344 bytes |
| Dark / Forest | 312 | 264.85 ms | 40.45 ms | 49.37 ms | 74,192 bytes |

Thirty process samples were collected during the sustained window. Maximum observed summed RSS was 53.91 MiB for the four vision helpers, 174.83 MiB for the dashboard, and 61.48 MiB for CLI/service processes including verification clients. Maximum observed CPU sums were 16.1%, 30.3%, and 3.5% respectively, using `ps` percentages. These are sampled maxima, not simultaneous peak totals or total private/GPU memory.

The host was already under memory pressure: a later sample reported pressure level 2 and 7866.50 MiB of swap in use. Four live screens work on this machine, but this run does not establish that four simulators plus the user's other apps avoid swapping. Fleet memory was about 4.0–5.0 GiB during these checks.

## Failures found and resolved

The first integration run stopped after a native text replacement returned “not acknowledged.” All four vision feeds continued. A fresh accessibility read confirmed that the requested text had arrived. The harness now records that exact ambiguous acknowledgment and observes the expected value without resending input. The passing four-phone run did not need this recovery path. This remains a limitation of the existing native input helper, not proof that acknowledgment alone establishes success.

The implementation arena rejected a candidate that could spin past `wait_frame` deadlines while retaining a reconnecting frame from another epoch. The selected implementation checks cancellation and live state before accepting a cursor.

Final review found that a requested timeout began after waiting for a retiring helper. The deadline now covers subscription and capture waiting. A regression test failed before the fix; retirement-timeout and automatic-subscription-cleanup tests passed repeated race runs after it. The fresh reviewer confirmed the finding resolved.

Newer P1 fixes landed in the checkout during the isolated implementation comparison. Three-way integration preserved their build isolation, native target validation, matrix reporting, and bounded RPC connection reuse. Vision watches use a separate HTTP transport so streaming does not occupy that control pool or change its timeout.

## Reproduce

With the four demo phones already running:

```sh
make test
make app
python3 scripts/verify-live-vision.py --seconds 60 --output .cache/live-vision-validation/four-live.json
```

The lifecycle check intentionally crashes and suspends the selected phone's **capture helper**, then shuts down and boots that managed demo phone under a lease. It requires exactly four managed, Booted, unleased phones:

```sh
python3 scripts/verify-vision-lifecycle.py --device EXACT_MANAGED_DEMO_UDID --output .cache/live-vision-validation/lifecycle.json
```

Raw evidence from this run is retained locally under `.cache/live-vision-validation/`: `four-live-confirmed.json`, its four JPEGs, `lifecycle.json`, `service-restart.json`, `idle-cleanup.json`, `final-check.json`, final build/test logs, and `dashboard-live.png`. The final installed build advanced all four streams, returned image-free 40 ms timeouts in 46–50 ms including CLI overhead, left all leases released, and preserved all 127 evidence files unchanged. The original failed run remains `four-live.json`. The scripts save bounded summaries and explicitly requested final JPEGs; production streams do not write frame files.

Private CoreSimulator interfaces still require compatibility checks after Xcode/runtime updates. Hidden-window pausing, configurable capture quality/rate, longer resource soaks, and multiple distinct Xcode project demonstrations remain future work. No comparative speedup or display-rate video claim is made.
