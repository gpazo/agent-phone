# Selected design

Use design A ownership boundaries with design B matrix-local output claims. Independent judge scored A 5/5/3/5/5 and B 4/4/3/4/4 for correctness, evidence, small API, invariants, artifact isolation. Both models are from one family, so this is not a cross-provider panel.

- Process-owned bounded HTTP client. Preserve readiness probing before each new call and never replay requests. Close idle transports after MCP workers finish.
- Preserve registry schema. Reconcile live runtime/type/name by exact registered UDID, and serialize lookup/create/acquire through one reservation owner.
- Normalize build identity and canonical output paths. Reject conflicting equal/nested claims within a matrix before builds. Snapshot app products/logs while holding host build lock. No permanent directory markers.
- Typed matrix status/counts, saved report, and partial result on failure. CLI/MCP report failure without discarding rows. Smoke wrapper retains the failed report.
- Native association checks app/context/raw semantics, ambiguity before dedup, and completeness. Keep beyond-250 candidates passive. Close helper after any validation error or invalid frame. No error taxonomy needed.
- Exact identical replacement between separate tree/receipt captures is unobservable from existing data. Do not claim atomic capture.

## Parent-reproduced red evidence

Transport: 12 completed sequential requests opened 12 Unix connections, want at most 1. Two waves of 8 concurrent requests opened 16 Unix connections, want at most 8.

Native: 16 mismatched/incomplete receipt cases obtained actionable tokens and emitted input against fake helper; five failed validation cases retained helper. Unchanged target/timer fixtures remained passing.

Matrix: wrong runtime/type/missing/duplicate/renamed target reached acquire sentinel. Result discarded partial report. Failed build lost rows. Aliased/nested output claims reached build. Later B build overwrote A product marker and log.

Smoke wrapper: expected matrixRun partial report was None before fix; focused Python suite passes after fix.

Parent ran each red check before worker implementation. No simulator operations occurred in these fixture runs.
