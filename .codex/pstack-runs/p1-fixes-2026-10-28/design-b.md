# Candidate design: close three P1 correctness gaps

## Problem

Each MCP request constructs a new transport, leaking idle Unix sockets. Matrix reuse checks only a name, products stay in mutable DerivedData paths, and failed rows report success. Go ignores fields in native receipts, while validation transport failures leave the helper attached. Preserve existing inputs, exact managed IDs/leases, persistent helpers, and sibling-independent target checks.

## Usage (caller's view)

Callers keep the current schema. One stdio session owns one RPC client; matrix failures retain every row and the report path. Mutations still require the exact device, lease, observation ID, and element reference:

```go
rpc, _ := OpenRPCSession(ctx); defer rpc.Close()
resp, err := rpc.Call(ctx, "matrix", json.RawMessage(`{"matrix":"examples/wikipedia.json"}`))
```

## Shape

Keep ownership in `cmd/agent-phone` for MCP transport, `internal/phone` for device/build/matrix policy, and the existing native helper for receipt completeness.

```go
// cmd/agent-phone
type RPCSession struct { client *http.Client; transport *http.Transport }
func OpenRPCSession(context.Context) (*RPCSession, error)
func (*RPCSession) Call(context.Context, string, json.RawMessage) (phone.Response, error)
func (*RPCSession) Close() // CloseIdleConnections

// internal/phone
type PhoneIdentity struct { UDID, Name, DeviceType, Runtime string }
type MatrixTarget struct { Device, Lease string }
func reconcileMatrixTarget([]PhoneIdentity, []LiveDevice, MatrixIdentity) (PhoneIdentity, error)
func (s *Service) reserveMatrixTarget(context.Context, MatrixIdentity, string) (MatrixTarget, error)
type MatrixResult struct { ID, Name, Report string; Counts MatrixCounts; Results []VariantResult }
type MatrixFailure struct { Report string; Failed int }
func normalizeBuildSpec(Params, string) (BuildSpec, error)
func canonicalOutputPath(string) (string, error)
func (s *Service) build(context.Context, BuildSpec) (BuildResult, error) // Apps point to immutable snapshots
type TargetReceipt struct { Tree map[string]any; Token, App, Context string; Complete bool }
func sameTarget(Element, TargetReceipt, string, int) bool
type BridgeFailure struct { Kind BridgeFailureKind; Err error }
```

`RPCSession` shares one bounded, concurrency-safe client per CLI/MCP lifetime and closes idle connections when workers finish.

Optional registry runtime/type fields keep old `phones.json` rows readable. Reconciliation requires a registered UDID and one live device matching name/runtime/type; missing or ambiguous metadata fails before boot. A narrow reservation lock covers check/create/acquire and returns the exact ID/lease pair for all mutations.

Canonicalize project and DerivedData paths through symlinks. Reject distinct fingerprints claiming one matrix output path before Xcode. Keep the local build mutex and add a host-scoped lock across service homes. Copy each `.app` to a unique immutable artifact path before another build can overwrite DerivedData; matrix installs use snapshots while the `derivedData` input keeps its meaning.

Persist the report before aggregate status. `phone.Result(value, err)` retains partial output; `MatrixResult` plus `MatrixFailure` makes CLI exit nonzero and MCP set `isError:true` while preserving rows. Build failures mark affected rows failed too.

For targets, store private ancestor context per element and count identity signatures in the observation. Accept only complete, local receipts agreeing on app, context, type, identifier-or-label, placeholder, and value; reject duplicate signatures. Ancestor-only context ignores changing siblings. `Bridge.Action` separates semantic rejection from broken transport: stale-target consumes the observation but retains a healthy helper; timeout, disconnect, malformed reply, or failed I/O closes it. Input stays one-shot.

## Tradeoffs accepted

- We accept artifact disk use for stable products; current run artifacts have no retention policy.
- We accept rejecting ambiguous legacy matches rather than guessing which same-named simulator to mutate.
- We accept a host lock around Xcode to preserve serial builds across service homes.

## Alternatives considered

- In-memory type/runtime fields alone leave old registry rows unverifiable and expose stale-identity policy to callers.
- Copying all DerivedData per variant duplicates large trees; immutable `.app` snapshots isolate only products callers install.
- Closing the native helper after any validation error wastes a healthy helper on ordinary semantic stale-target rejection; typed failure classification keeps this decision inside `Bridge`.

## Deterministic fixtures and live proof

Fixtures: fake concurrent HTTP calls prove bounded connection reuse and ID routing. Old/new registry plus live-device JSON cover changed type/runtime, missing IDs, duplicates, and concurrent reservations. Symlink aliases must fail preflight; mutating a source `.app` after snapshot must leave its staged marker unchanged. Inject row failures and require a partial report, nonzero CLI, and MCP error. Table-drive receipt mismatches, incompleteness, duplicates, same-label replacement, and changing siblings; only transport failure discards the helper.

Live proof: repeat rejected MCP observations and check descriptors stay bounded. Use only explicit managed IDs and leases. Verify shared DerivedData claims fail preflight and feature-marker products remain distinct after later concurrent builds. On leased Wikipedia, reject stale app/context/value and duplicate labels, but accept a target despite an unrelated ticking label. After validation timeout, confirm observation reconnects without input replay; record AX and screenshot times separately.

## Open questions and risks

- Does pinned `simctl list devices --json` expose a stable type identifier for every registered device? Otherwise use a read-only metadata query and fail closed when type cannot be proven.
- Should immutable snapshots remain with current artifacts until a future retention policy, or be removed after every matrix row completes?

## Next implementation step

Implement session-owned transport reuse and partial response preservation, then wire the pure reconciliation, path, and receipt checks into matrix and native flows.
