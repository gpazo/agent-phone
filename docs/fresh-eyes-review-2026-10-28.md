# Fresh-eyes review of agent-phone

Reviewed on October 28, 2026 using three independent pstack exploration slices and a separate synthesis review. This is a recommendation report. Product source, dependencies, simulator state, and existing documentation were not changed.

The highest-value work is to make long-running control and test results more trustworthy, then expose the upstream capabilities that simplify daily use. Keep the provider-independent Go service, explicit managed devices and leases, persistent helpers, and separate accessibility and image captures.

## Evidence and scope

The reviewed SimSlim checkout is clean at `2c68da93700aa1a1c45ce31a7ea9f7cac2174ddc`. Of 42 vendored files, 41 match it byte for byte. Only the reduced `go.mod` differs. The reviewed Jev native source is at `1606fbbe91e956b5c1e6fc481d5a3fcfc9f85b90`; both imported native implementation files match. These are the revisions already recorded in [THIRD_PARTY.md](/Users/gp/src/agent-phone/THIRD_PARTY.md). No upstream fetch was needed. Jev has unrelated local documentation changes and artifacts; those were not used as implementation evidence.

Agent-phone has no initial commit, and its product files are untracked. Findings refer to the current working files. This review preserved them.

Checks performed:

- `make test` passed. The Go race result was cached; the root package has eight tests and the Python harness has four. `cmd/agent-phone` has no test files.
- The nested SimSlim module passed a separate `../../.tools/go/bin/go test -race ./...` from `third_party/simslim`. Root `go test ./...` does not enter this nested module.
- The existing CLI returned the fleet with four booted managed Wikipedia phones. MCP initialization, discovery of 14 tools, and ping passed.
- A persistent MCP process made twelve rejected observations of a nonexistent device. Its open Unix descriptors grew from zero to twelve. No lease was acquired and no simulator was changed. See [recorded socket evidence](/Users/gp/src/agent-phone/.codex/pstack-runs/fresh-eyes-2026-10-28/mcp-sockets.json) and the [reproduction script](/Users/gp/src/agent-phone/.codex/pstack-runs/fresh-eyes-2026-10-28/check-mcp-sockets.py).
- One ordinary four-phone `list` response contained 125,046 bytes across 4,303 lines, mostly cached accessibility elements. This is a response-size observation, not a performance benchmark. See [baseline evidence](/Users/gp/src/agent-phone/.codex/pstack-runs/fresh-eyes-2026-10-28/baseline.json).

No Xcode build, native input, new Wikipedia search, dashboard interaction, or offline reinstall was performed. Source-level findings below are not claims of live reproduction.

Pstack's **Prove It Works** principle led to the real MCP socket check. **Test Behavior, Not Implementation** shaped the proposed regressions around observable outcomes, such as wrong binaries, retained sockets, rejected stale targets, and partial reports.

## Fix correctness before adding controls

| Priority | Finding and evidence | Recommended change | Acceptance check |
| --- | --- | --- | --- |
| P1 | Each [RPC request creates a new HTTP transport](/Users/gp/src/agent-phone/cmd/agent-phone/main.go:149), whose idle connections remain open. This was reproduced in persistent MCP. | Reuse a client/transport with bounded idle lifetime and explicit cleanup. | Repeated and concurrent requests through one MCP process keep connections bounded and route each response to its request ID. |
| P1 | [Matrix device reuse matches only the name](/Users/gp/src/agent-phone/internal/phone/matrix.go:141). Changing the manifest runtime or device type can reuse the previous simulator. Concurrent runs can create duplicate names. | Reconcile with live device metadata and atomically reserve an exact managed target. Reject ambiguous or incompatible reuse before mutation. | Changed runtime/type, missing registered device, duplicate names, and concurrent identical manifests cannot select the wrong target. |
| P1 | [Explicit DerivedData bypasses default build isolation](/Users/gp/src/agent-phone/internal/phone/build.go:63). The matrix [builds every variant before installation](/Users/gp/src/agent-phone/internal/phone/matrix.go:85). Distinct builds sharing an output directory can overwrite earlier app bundles. The README warns against this, but the API accepts it. | Normalize build specs and reject conflicting output-directory claims. Use immutable app products if later builds can overwrite artifacts still needed by a run. | Distinct feature markers survive building and installation. Conflicting paths fail before Xcode starts. Cover concurrent later builds too. |
| P1 guard gap | [Token assignment](/Users/gp/src/agent-phone/internal/phone/observation.go:176) compares type, optional identifier, and label/placeholder, but ignores the native receipt's app/context. A transition between tree and target capture can associate a new same-labelled control with an old description. No wrong-target action was reproduced. | Adapt [Jev's scoped-token eligibility](/Users/gp/src/jev-vphone-cli/sources/vphone-cli/VPhoneJevSimulator.swift:269), including app/context/value agreement and ambiguity/completeness checks. Keep validation scoped to the selected target. | Changed app, row context, value, duplicate identity, and same-label replacement fail closed. An unrelated ticking label still allows the selected action. |
| P2 | [Matrix returns a successful RPC envelope](/Users/gp/src/agent-phone/internal/phone/matrix.go:122) even when variants fail. CLI exits zero and MCP says `isError:false`. Failures remain in the rows; the smoke harness explicitly checks them. | Add a typed aggregate outcome with counts and report path. Make matrix task failure explicit in CLI and MCP while preserving partial results. | Boot, install, assertion, or cleanup failure produces a failed aggregate result and retains all completed evidence. |
| P2 | [Failed-hit elements remain in the tree](/Users/gp/src/agent-phone/internal/phone/observation.go:180), while [matrix expectations call any matching label/value visible](/Users/gp/src/agent-phone/internal/phone/matrix.go:264). Tree presence does not prove visibility. | Record evidence provenance. Separate tree-presence assertions from assertions requiring verified visibility. | A label behind a modal cannot certify visible success. Passive text remains usable evidence without being treated as actionable. |
| P2 | [Selected-target validation errors return without closing the bridge](/Users/gp/src/agent-phone/internal/phone/observation.go:250). A timeout or malformed response can leave a late reply on the persistent stream. | Discard transport-corrupted helpers, as [Jev's request code does](/Users/gp/src/jev-vphone-cli/sources/vphone-cli/VPhoneJevPreferences.swift:133). Distinguish semantic target rejection if healthy connections should remain open. | A delayed or malformed validation reply cannot be consumed as the next capture response. Recovery observes afresh and never replays input. |

## Useful capabilities to adopt

| Improvement | Source and user benefit | Scope and tradeoff |
| --- | --- | --- |
| Simulator capability preflight | SimSlim already has [feature resolution and diagnosis](/Users/gp/src/simslim/features.go:65) and [profile verification](/Users/gp/src/simslim/verify.go:18). Let matrices declare required features such as StoreKit or universal links, retain the effective profile in reports, and explain missing services before a long run. | Small-to-medium integration. The matrix currently supplies only device and lease to boot, so its configuration must gain explicit profile fields. Diagnosis proves service configuration, not end-to-end feature behavior. |
| Read-only `doctor` and useful CLI help | Report selected Xcode/runtime, helper paths and versions, daemon identity/root/budget, and profile drift. Generate method help from the existing schema, and accept JSON from stdin. | Low-cost developer UX. Diagnostics must avoid booting devices or repairing state implicitly. |
| Verified scroll anchors | [Jev chooses an appropriate content anchor](/Users/gp/src/jev-vphone-cli/sources/vphone-cli/VPhoneJevSimulator.swift:245) when a scroll-view centre hits a child. Agent-phone currently offers scrolling only when the centre matches the container itself. | Medium native-control work. Preserve mandatory target validation and exclude keyboard anchors. Independently verify changed content, stale-anchor rejection, and non-scrollable panes. |
| Bounded observation-only waiting | [Jev's settling tests](/Users/gp/src/jev-vphone-cli/tests/JevTests/StableObservationTests.swift:79) distinguish a changed/stable native layout from an input acknowledgment. Add an optional bounded wait for an expectation instead of duplicating polling in every client. | Medium scope. Wait on observation predicates with deadlines and cancellation. Do not retry input or promise that stable accessibility means every animation has finished. |
| Small fleet summaries and honest evidence age | Default fleet output can contain runtime, short UDID, owner/busy state, memory, capture timestamps, screenshot path, and control count. Full cached trees can be an explicit detail option. | Low-to-medium schema change. [Fleet currently emits a new `measuredAt` for cached memory](/Users/gp/src/agent-phone/internal/phone/service.go:472). Preserve the real sample timestamp and mark partial/unknown memory. Admission already requests fresh measurements. |
| Better dashboard failures and previews | [SimSlim preserves subprocess status and output](/Users/gp/src/simslim/gui/Backend.swift:150). Agent-phone discards stderr/status and does not decode screenshot time or measurement errors. Show real diagnostics, separate AX/image ages, and stale data after refresh failures. | Small Swift change. Keep cached previews inexpensive. Busy-state previews can remain visible with explicit age rather than disappearing. |
| Verified offline setup recovery | Reuse checksum-verified archives already in `.cache`, download to temporary files, and promote them only after verification. [Current setup redownloads when executables are missing](/Users/gp/src/agent-phone/scripts/setup.sh:9). | Small shell change. Test valid-cache restoration without network, corrupt-cache failure, and preservation of a valid archive after download failure. |

## Testing should become a product capability

Use a small injectable command runner and helper transport at the existing external boundaries. Avoid a general backend framework. SimSlim's fake command tests and Jev's [persistent fake-socket tests](/Users/gp/src/jev-vphone-cli/tests/test_jev_commands.py:125) are useful patterns.

The fast suite should exercise CLI exit status, malformed JSON, concurrent MCP IDs, image bytes, startup errors, sustained connections, matrix reuse/build identity, cancellation, and partial result preservation. It should prove that no real simulator commands execute. Include the nested vendor suite explicitly and keep builds serial.

Then add explicit opt-in native checks modeled on Jev's [reference guards](/Users/gp/src/jev-vphone-cli/tests/NativeValidation/guards.py:39), [text checks](/Users/gp/src/jev-vphone-cli/tests/NativeText/check.py:62), and [scroll checks](/Users/gp/src/jev-vphone-cli/tests/NativeScroll/benchmark.py:73). Adapt them to managed UDIDs and leases. Cover Unicode, consumed tokens, stale values, moving/recycled cells, app restarts, overlays, and unrelated changing labels. Keep Wikipedia as the official end-to-end demonstration and use deterministic local fixtures for mechanics coverage.

This matters because [Jev documents scoped native references as experimental and off by default](/Users/gp/src/jev-vphone-cli/tests/NativeValidation/README.md:3), while agent-phone uses that mechanism as its sole input path. The existing Wikipedia runs establish useful narrow evidence, not arbitrary-app equivalence.

## Follow-up questions that require evidence

- `Tree` does not request `IsEnabled`, though `flatten` checks it. Jev has the same omission. Inspect the pinned helper's actual response before claiming disabled controls are exposed.
- Test delayed shutdown before adding retries for transient `memory_unknown`. SimSlim provides `WaitShutdown` already.
- A build mutex belongs to one service, while daemon locks belong to configurable data directories. Test two services before claiming host-wide Xcode serialization. A shared host build lock would enforce that policy across homes.
- MCP currently starts unbounded request goroutines and ignores cancellation notifications. Exhaustion was not tested. Add bounded work and cancellation after the connection and outcome fixes.

Do not copy SimSlim's fleet-wide watcher unchanged. It can operate on unleased, unmanaged devices. Cloning also changes source state and requires an explicit lease-aware design. Automatic deletion, VM firmware/signing requirements, and model/inference dependencies do not belong in this improvement pass.

Recommended implementation order is transport lifetime and offline regression tests, matrix identity/outcomes/artifacts, native target association and mechanics tests, then diagnostics and daily-use features.
