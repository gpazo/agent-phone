# P1 repair validation — 2026-10-28

The three P1 findings from the fresh-eyes review are repaired: persistent MCP connection growth, matrix target/build/result isolation, and unsafe association between accessibility observations and native target receipts.

## Method

The pstack bug-fix workflow supplied separate design candidates, an independent design judgment, isolated implementation workers, failing tests before fixes, parent reproduction, a comment review, and a fresh correctness review. The design candidates used two models from the same provider; this was not a cross-provider review. Domain types describe native identity and matrix outcomes. Regression tests exercise input dispatch, connection counts, artifact contents, and public error envelopes.

The user checkout had no initial Git commit and contained untracked project files. It was preserved. Red and green commits live only in an ignored snapshot repository under `.cache/p1-fixes-2026-10-28`; no user-checkout commit or PR was created. Plans, patches, review decisions, and test logs are under `.codex/pstack-runs/p1-fixes-2026-10-28`.

## Transport

The CLI previously constructed a new HTTP transport for every request. A persistent MCP process retained each idle Unix connection. It now shares a process-owned transport with at most eight connections and a 90-second idle timeout. Service readiness is still checked before a new call. Mutations are not replayed.

Failing regression output:

```text
12 completed sequential requests opened 12 Unix connections, want at most 1
two waves of 8 concurrent requests opened 16 Unix connections, want at most 8
```

Both regression tests pass after the fix. The original live check, twelve rejected observations through one persistent MCP process, changed from twelve open Unix descriptors to one:

```json
{"calls":12,"unix_fds_before":[],"unix_fds_after":["f4"]}
```

The check performs no simulator mutation. Its script is `.codex/pstack-runs/fresh-eyes-2026-10-28/check-mcp-sockets.py`; before and after results are saved in the corresponding review and repair directories.

## Matrix and builds

Matrix reuse previously matched registry names without verifying the live simulator identity. Builds returned mutable DerivedData paths, and a later build could overwrite an earlier variant's app and log. Matrix row failures still produced a successful outer envelope, while early build failures lost the report.

Failing regression output included:

```text
partial report lost: {OK:false Result:<nil> Error:one variant failed}
build A changed: marker="B" err=<nil>
build A log changed: "build B\n" <nil>
```

The repaired path validates the live registered UDID, name, runtime, type, and availability before building and again at reservation. Lookup/create/acquire is serialized. Build specifications and paths are normalized, conflicting output claims are rejected, and a host lock covers Xcode execution through immutable app/log publication. Typed reports retain every row and make failed/skipped outcomes fail the CLI and MCP envelope.

Live checks used a small UIKit test fixture on two existing, leased Wikipedia simulator IDs. The official demonstration remains Wikipedia. Builds were serial, with two Xcode jobs each. Evidence is `.cache/p1-fixes-2026-10-28/matrix-live-evidence.json`.

| Check | Observed result |
| --- | --- |
| Wrong runtime on existing targets | Two failed rows; no build or acquisition |
| Wrong device type on existing targets | Two failed rows; no build or acquisition |
| Nested output claims | Failed/skipped rows before creating DerivedData |
| Failed matrix through CLI/MCP | CLI exit 1; MCP `isError:true`; partial report retained and equal to the saved JSON |
| Build A then B with the same explicit DerivedData | Returned A app still contained A; B contained B; log paths differed |
| Matrix with distinct A/B builds | Both passed; each phone's accessibility tree contained its own expected variant label |
| Deliberately absent assertion, `keepRunning:false` | Failed report retained; phone shut down and lease released |

The test fixture initially used a generated Info.plist setting that Xcode did not emit. The fixture was corrected to use an explicit plist with a substituted build setting before the live app-isolation assertions. This was a test-fixture issue, not a product success.

Independent review also identified a case-alias gap on this Mac's case-insensitive APFS volume. Existing, missing, and nested case aliases reached two fake builds before the follow-up fix. The project case alias also produced different cache keys. Those cases are covered by dedicated regressions.

The follow-up uses macOS `F_GETPATH` for existing path spelling and `_PC_CASE_SENSITIVE` for missing components. The case-alias regressions pass. Existing Unicode paths are supported; on a case-insensitive volume, a new non-ASCII output directory must be created before either a standalone build or a matrix can use it. This avoids guessing APFS Unicode equivalence. The preservation test for distinct names on a case-sensitive volume skipped on this host.

The rebuilt service also rejected existing, missing, and nested case aliases through the real CLI before any build. A real fixture build succeeded through an uppercase project alias into an existing `Données` output directory. Evidence is `.cache/p1-fixes-2026-10-28/case-live-evidence.json`.

## Native observations and recovery

Receipt association previously omitted app, ancestor context, value, class, and exact identifier-presence checks. Deduplication hid ambiguous targets, and incomplete visible trees could still offer input. Failed native validation left the helper attached to the phone.

Sixteen red receipt cases received actionable tokens and sent input; five validation-failure cases retained their helper. A representative failing assertion was:

```text
unsafe input accepted: error=<nil> inputs=1
```

These regressions now pass. Association checks raw semantic attributes, app, and context; counts ambiguity before deduplication; and keeps incomplete visible trees passive. Context follows the native helper's ancestor, keyboard, unnamed-cell, back-navigation, and UTF-16 truncation rules. Valid targets beyond the 250-point batch stay passive. Offscreen remote content does not disable valid local targets. A changing unrelated label does not invalidate the selected target.

Validation errors or malformed frames consume the observation and close the helper. Pipe-backed malformed JSON, oversized responses, and EOF tests verify closure with no input dispatched. The two vendored native Objective-C source files are unchanged.

## Wikipedia live validation

The combined binary was built with `make build`. Before restarting the specific agent-phone daemon, all four managed phones were checked for active ownership and busy state. The replacement service retained the 6144 MiB budget. Unmanaged simulators were not changed.

The first four-phone run failed immediate native text readback on three phones. Fresh leased observations then showed all four requested preflight values present and actionable. This is the unchanged native helper's immediate readback behavior: a failed response does not prove that text was not entered. No input was automatically retried.

After inspecting those states, a new run used different queries: Mercury, Ocean, Venus, and Mountain. All four passed with their own input and result labels, overlapping search intervals of 869.58 ms, stale-observation and lease-conflict rejection on every phone, two samples per capture mode, and MCP text/JPEG verification on every phone. One observation error recovered through the harness's bounded read-only polling; the harness does not retain its detailed error. The run took 28.21 seconds.

Evidence:

- `.cache/p1-fixes-2026-10-28/wikipedia-four-smoke.json`: initial failed run.
- `.cache/p1-fixes-2026-10-28/inspect-<UDID>.json`: post-failure state inspection.
- `.cache/p1-fixes-2026-10-28/wikipedia-four-smoke-warm.json`: passing run.

The matrix cleanup test subsequently restored its phone to Booted, launched Wikipedia on both fixture phones, and released its leases. All four managed phones were Booted and unowned after validation.

After the final case-path rebuild and daemon restart, fresh screenshots and actionable Wikipedia observations were obtained on all four phones. An initial read returned `window-server frontmost returned no application object`; a subsequent bounded observation-only pass succeeded, with one additional transient read error on the light phone. No input retry was used. Final evidence is `.cache/p1-fixes-2026-10-28/final-observations.json`.

## Final checks

`GOMAXPROCS=2 GOFLAGS='-p=1' make test` passed the Go race suite and five Python tests. The case-sensitive-volume fixture was skipped on this case-insensitive host. The Python suite deliberately prints a failed smoke-report status while asserting failure-report preservation; the suite itself passes.

```text
ok  	agent-phone/cmd/agent-phone	(cached)
ok  	agent-phone/internal/phone	2.365s
Ran 5 tests in 0.002s

OK
```

`make build` compiled the CLI and native helper. Scoped whitespace checks passed. The comment reviews found no changes to request. The independent correctness review's case-alias finding was repaired and re-reviewed with no remaining actionable finding. Final test/build logs, red outputs, and patches remain in `.codex/pstack-runs/p1-fixes-2026-10-28`.

## Remaining limits

The tree and native receipt captures are separate and lack a shared native identity. Semantically identical replacement between captures cannot always be detected. Screenshot and accessibility timestamps remain separate. No atomic-capture or input-acknowledgment success guarantee is made.

Immutable app snapshots and logs have no retention policy yet. Live validation covers one Xcode project with distinct feature builds on iOS 26.5, not every project/runtime combination. Case-sensitive-volume behavior is only claimed where an actual check ran. The immediate native text-readback timing limit remains documented above.
