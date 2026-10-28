# P1 design A: ownership at the existing boundaries

Phase checklist: [x] Ground; [x] Sketch; [ ] Agree/synthesize (parent); [ ] Implement (parent); [ ] Scrap if evidence contradicts the sketch.

## Usage (caller's view)

Keep existing manifests and `phones.json`. Failed matrices preserve their report and yield CLI nonzero / MCP `isError:true`.

```go
// main.go: one owner per CLI/MCP lifetime; construction does not start the daemon.
rpc := newRPCClient(socket, ensureService)
defer rpc.Close()
reply, err := rpc.Call(ctx, method, params) // shared safely by MCP workers

// matrix.go: reserve returns an exact managed UDID AND its lease.
target, err := s.reserveMatrixTarget(ctx, spec, owner)
result, err := s.Call(ctx, Request{"install", Params{
    Device: target.UDID, Lease: target.Lease.Token, App: build.Apps[0].Path}})

// observation.go: only the association function can grant an actionable token.
elements := associateTargets(snapshot, receipts)
```

## Problem and grounding

`main.go` leaks per-request transports. Matrix prebuilds mutable artifacts, caches unnormalized keys, and separates name selection from acquisition. Build serialization covers one Service. `Result` discards partial values on error. Observation association ignores receipt semantics; validation errors leave its stream alive.

Preserve existing ownership: registry → managed IDs; Phone mutex → lease/helper/observation; native token → selected ancestry and immediate display hit. SimSlim Device omits device type: decode live catalog narrowly in phone.

## Shape

```go
// cmd/agent-phone/main.go (or client.go in the same package)
type rpcClient struct { http *http.Client; ready sync.Once; readyErr error }
func newRPCClient(socket string, ensure func(context.Context) error) *rpcClient
func (c *rpcClient) Call(context.Context, string, json.RawMessage) (phone.Response, error)
func (c *rpcClient) Close() // closes idle connections after workers finish

// internal/phone/matrix.go; private domain types, no wire maps in planning.
type deviceSpec struct { Name, RuntimeID, DeviceTypeID string }
type reservedTarget struct { UDID string; Lease Lease }
func (s *Service) reserveMatrixTarget(context.Context, deviceSpec, string) (reservedTarget, error)
type MatrixCounts struct { Passed, Observed, Failed, Skipped int }
type MatrixReport struct { /* existing id/name/completedAt/results/report JSON fields */; Counts MatrixCounts }

// internal/phone/build.go
type buildSpec struct { Project, Scheme, Configuration, DerivedData, Identity string; Settings map[string]string }
func normalizeBuild(Params, string) (buildSpec, error)
func validateBuildClaims([]buildSpec) error
func (s *Service) buildProduct(context.Context, buildSpec) (BuildResult, error)

// internal/phone/observation.go; private evidence used by Element.
type targetIdentity struct { Automation int; Class, Identifier, Label, Placeholder, Context, Value string }
type treeSnapshot struct { App string; PID int; Complete bool; Elements []Element }
type targetReceipt struct { App, Context, Token string; Identity targetIdentity }
func associateTargets(treeSnapshot, []targetReceipt) []Element
```

The HTTP owner lazily ensures readiness, shares a bounded transport, consumes/validates bounded responses, and closes idle connections after workers finish. Never retry an RPC. MCP initialization remains independent of daemon startup.

Reservation holds a stable user-scoped file lock from live catalog inspection through lease creation. Require one compatible registered ID; reject duplicate names, disappeared IDs, runtime/type mismatch, and unmanaged same-name collisions. Otherwise create/register/acquire before unlocking. Ordinary create shares this lock. Canonicalize data-directory aliases before daemon locking; preserve registry schema and derive metadata live. Share acquire/expiry logic; retain per-phone mutation locks.

Normalize before caching/building: default Debug, canonical project/output paths, copied settings. Resolve symlinks through the nearest existing ancestor; reject equal/nested output claims for different identities. Persist a DerivedData ownership marker to reject conflicting later requests. A user-scoped Xcode file lock independent of `AGENT_PHONE_HOME` encloses claims, build and publication. Reject output-redirecting settings. Copy bundles/logs to fresh per-build directories, atomically publish, and return only those stable paths. No artifact pruning.

Start the report before builds; retain every row, build failure and skipped dependency. Finish cleanup before counts. Persist and return `(report, matrix_failed)` for failed rows. `Result` retains values on errors; existing CLI/MCP error handling then suffices.

Tree decoding derives native-compatible ancestor context: last two nonduplicate ancestor labels, keyboard grouping, unnamed-cell direct text, and back-navigation suffix. Match native UTF-16 truncation. Compare app, exact identifier presence/value, type/class, label/placeholder, normalized value, and context; require a nonempty token and unique semantic identity counted before flattening deduplication. Reject actionable eligibility for incomplete trees, unresolved visible remote content, malformed batches, or more than 250 candidates. Passive elements remain observable. Unrelated sibling labels never enter the selected identity. Preserve separate AX/screenshot timestamps.

Every validate-target error closes/clears the bridge and consumes the observation. Even semantic rejection closes conservatively, avoiding a new taxonomy. Fresh observation may restart helpers; never replay input. Healthy helpers remain persistent.

## Tradeoffs and alternatives

- Accept disk copies for stable install products and bounded memory; hard links would share later writes.
- Accept persistent path claims and conservative target rejection for reviewable ownership. Releasing claims after builds would permit incompatible reuse and require more coordination.
- Reject a generic backend framework: it exposes backend policy through additional interfaces. Inject only command execution and helper transport at existing external boundaries.
- Reject build-then-install streaming as the sole fix: concurrent builds and retained paths would still race.

## Proof and limits

Deterministic fixtures must cover concurrent MCP IDs and bounded accepted sockets; canonical path aliases/default configuration/overlapping outputs; two service homes contending for the same host build lock; variant marker A surviving later build B; exact runtime/type, missing/duplicate/unmanaged target and concurrent reservations; boot/install/assert/cleanup failures retaining all report rows and producing CLI nonzero/MCP error. Fake commands must never dispatch real simulator commands.

Use recorded tree/receipt fixtures for app/context/value/identifier changes, duplicate identities before deduplication, completeness gaps and unrelated timers. A fake helper emits delayed, malformed, oversized and semantic validation failures; assert closure, zero input, fresh next helper and no late-reply reuse.

Live proof: `make test`, relevant vendor tests, sustained MCP socket check, serialized feature-marker builds, then leased managed Wikipedia actions and intentional matrix failure. Record actual IDs, separate capture times, outcomes and limits.

Residual question for synthesis: tree snapshots contain no shared native object identity. Exact semantic duplicates across time can be indistinguishable during the tree-to-receipt gap; do not claim the semantic join proves atomic capture or rejects every identical replacement. Native object ancestry still rejects replacement after receipt capture. Stronger guarantees require enriching the shared capture protocol, beyond a silent comparison tweak.

## Synthesis decision and next step

Parent owns synthesis. Start with the reusable client and fake Unix-server regression, then implement matrix claims/reservations and strict receipt association against this contract.
