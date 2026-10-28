# Four-simulator validation — 2026-10-28

The final `make demo-four` run passed. Four iPhone 17 Pro simulators on iOS 26.5 ran Wikipedia 7.9.3 independently on the 8 GiB Mac14,2. All four remain booted, with leases released. The native dashboard was reopened and visually checked with all four previews visible together.

The service was restarted with an explicit `AGENT_PHONE_BUDGET_MIB=6144` after checking that no phone had an owner. The default remains half physical RAM. No unmanaged simulator was mutated. The same existing memory-admission and critical-pressure logic applies; this change does not claim reduced memory use or a hard RAM cap.

## Results

| Phone | Query | Result | Native observation median (range) | With JPEG median (range) |
| --- | --- | --- | ---: | ---: |
| Wikipedia / Light | Saturn | passed | 156 ms (147–232) | 451 ms (322–1977) |
| Wikipedia / Dark · Large text | Volcano | passed | 144 ms (136–232) | 485 ms (379–2005) |
| Wikipedia / Light · Large text | Jupiter | passed | 130 ms (121–192) | 492 ms (442–2085) |
| Wikipedia / Dark | Forest | passed | 147 ms (142–229) | 484 ms (377–2034) |

The four search-operation intervals had **1330.05 ms of common overlap**. Every worker entered and observed a distinct nonempty preflight value, waited at a barrier, then entered its assigned query. Each verified its own text-field value and an exact result label. After sampling, another barrier held all four sessions until final state checks excluded the other phones' query values. Unique device IDs and lease tokens were used throughout.

There were **73 explicit smoke observation requests**, **0 observation errors**, and **8 expected rejections** (one competing lease and one superseded observation per phone). Native action acknowledgment alone was not used as proof. MCP initialization and tool listing succeeded, and all four MCP observations returned the correct device, JSON semantics, and a complete JPEG identical to the screenshot file bytes.

The final fleet reading was **5.46 GiB** against a **6 GiB** admission budget. Host pressure checks returned level 2, not the critical level 4. Host swap usage rose from about 3.1 GiB before expansion to about 5.7 GiB after the final run. Four phones work here, but they put substantially more pressure on this 8 GiB host. The budget only governs admission and can be exceeded by application growth. The pre-existing pressure probe also fails open if sysctl cannot be read; this task did not redesign that policy.

Timings above are five successful samples per mode per phone from a single functional run. Native-only and JPEG observations alternate, with concurrent workers. Service timings exclude CLI startup/transport; client timings, ranges, and every sample are in the JSON report. The first JPEG samples were around two seconds and later samples were shorter. These observations do not establish a speedup or a sustained-throughput limit. No causal performance profile was taken. The Mac was also running its normal desktop applications, so comparisons to the earlier two-phone sample would be uncontrolled.

## Verification and fixes

- `make test` passed eight Go behavior tests with the race detector and four Python harness tests. The Python tests cover four distinct running devices, duplicate queries, duplicate names, and lease cleanup/barrier cancellation on worker failure.
- CLI, native helper, and SwiftUI dashboard builds passed.
- A complete `make demo-four` passed after the fixes below. The final report completed at 2026-10-28T07:37:46.871526-07:00.
- Empty-string replacement was rejected by Wikipedia's native accessibility setter in the initial harness. The test now uses an acknowledged nonempty preflight value and checks it exactly, ensuring repeated runs perform a real replacement. No unsupported empty-string success is claimed.
- A later setup rerun exposed the three-attempt startup read limit. The matrix now waits up to 30 seconds for its first successful accessibility observation, with bounded observation-only retries. It never retries input automatically.
- The native dashboard uses compact previews when more than two managed phones exist. A wider window shows all four at once.

## Pstack review

Pstack's Swarm workflow used two read-only exploration workers and two successive focused review workers. The parent owned every source edit and live simulator operation. No worker wrote shared source files or manipulated a simulator. All workers returned, with no dropouts.

| Coverage | Outcome |
| --- | --- |
| Capacity policy | Existing explicit 6144 MiB override selected; conservative default preserved. |
| Manifest, synchronization and dashboard | Four distinct variants, barriers, overlap proof, partial reports, compact previews implemented. |
| First focused review | Found the unsupported clear operation and insufficient clear assertion; both corrected. MCP validation strengthened. |
| Final focused review | PASS from current source and final evidence. Four setups, four searches and four MCP checks passed; no remaining blocker found. |

The Explain the Number principle changed reporting: actual memory, swap, sample ranges and limits are recorded; four-phone success is not presented as a memory optimization. Unit tests were run by the parent, not by the read-only reviewers.

## Local evidence

- `.cache/wikipedia-four-smoke.json` is the complete final successful report, including matrix setup, all observations, screenshots, timings and fleet state.
- `.cache/four-final.log` is the successful `make demo-four` output.
- `.cache/four-readiness-build.log` contains the final Go/Python tests and CLI/native build.
- `.cache/four-build.log` contains the SwiftUI dashboard build.
- `.cache/wikipedia-four-initial-failure.json` and `.cache/wikipedia-four-startup-failure.json` preserve the two issues found during verification.
- `.cache/four-host-samples.json` and `.cache/four-final-host.json` contain host snapshots collected during setup/retries, not a continuous full-run trace.
- `.codex/pstack-runs/four-simulators/plan.md` records the delegation scope and decisions.

This validates four independent instances of one app project on one runtime. Mixed Xcode projects, longer sustained sessions, and other Macs/runtimes remain untested.
