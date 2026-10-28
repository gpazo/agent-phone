# Candidate A: query-directed observation transactions

## Problem

The service should own bounded, observation-only waits because it already owns the phone lease, persistent helper streams, and consumable observation. A client loop cannot bound the current mutex wait or helper I/O, and the matrix currently calls accessibility-tree presence “visible.” This design retains the existing exact managed UDID, lease, per-phone serialization, and selected-target action validation. Its central abstraction is an observation transaction: acquire ownership within one deadline, repeatedly construct private samples, and publish exactly the sample that satisfies a small positive query before the deadline.

The parent’s live evidence adds a crucial constraint beyond the original grounding. `Covered evidence` was fully hidden by an opaque UILabel but remained in the AX tree, and `capture-targets` at its center still returned that label, identifier, and a token. Evidence: `.cache/ui-waits/baseline-assert.json`, `.cache/ui-waits/native-hit-before.json`, and the parent-owned WaitFixture. Consequently, **native AX hit verification is not visual visibility verification**. Neither a “visible” predicate nor a “verified visible” result is honest with the current backend. This candidate deliberately names the available evidence `tree` and `native-hit`; actual pixel visibility remains unknown.

## Usage (caller's view)

### README contract

`wait` repeatedly observes one explicitly leased managed phone until every condition matches. All selector fields are positive exact equalities. `evidence: "tree"` requires a matching element in the normalized accessibility tree. `evidence: "native-hit"` additionally requires a matching native AX center-hit receipt associated with the element’s raw semantics, app, and ancestor context. This does not establish pixel readability, unobscured visibility, full-element visibility, screen-wide stability, or an atomic AX/image capture.

```sh
agent-phone call wait '{
  "device": "EXACT-MANAGED-UDID",
  "lease": "LEASE-TOKEN",
  "conditions": [
    {"where": {"role": "text", "label": "Ready"}, "evidence": "native-hit"}
  ],
  "timeoutMs": 5000,
  "screenshot": true
}'
```

Successful response, abbreviated:

```json
{
  "ok": true,
  "result": {
    "attempts": 4,
    "elapsedMs": 914,
    "satisfied": [0],
    "observation": {
      "id": "observation-id",
      "capturedAt": "AX-response-received-time",
      "screenshotAt": "later-screenshot-completed-time",
      "elements": [{
        "ref": "observation-id:e4",
        "role": "text",
        "label": "Ready",
        "nativeHitAt": "native-batch-response-received-time",
        "actionable": false
      }]
    }
  }
}
```

Every condition must explicitly name its evidence; there is no misleading visibility default. `mode` defaults to `all`; `mode: "any"` allows one flat list of alternatives. `timeoutMs` defaults to 30000 and accepts 1–30000. `screenshot` defaults to false and captures one image only after a sample satisfies the query. The image must complete within the same budget. Polling uses an internal 250 ms minimum delay after each unsuccessful attempt; there is no public polling, settling, retry-count, or stability knob.

A condition is existential: one matching element is enough. A wait does not promise selector uniqueness. The returned observation retains all elements and their evidence; a caller intending to act still selects one verified actionable ref and invokes `act`, which validates the selected target again immediately before input. Passive text can have `nativeHitAt` and still has `actionable:false`, no actions, and no authority to receive input.

`wait_timeout` is an unsuccessful API response, with bounded diagnostics containing attempts, elapsed time, the satisfied condition indexes from the last completed sample, and the last transient observation error. It does not return an actionable observation. Cancellation is `cancelled`, not success. An input is never part of the wait, so neither an input acknowledgment nor a failed input can be replayed by waiting.

### Call site 1: Python Wikipedia flow

The smoke harness’s `wait_for(args, lambda...)` is removed, along with its broad RuntimeError retry loop. It uses the service’s exact same request shape through its existing `call` helper:

```python
def await_ui(args, conditions, *, mode="all", timeout_ms=30000):
    return call("wait", dict(args, conditions=conditions, mode=mode,
                             timeoutMs=timeout_ms))["observation"]

def hit(where):
    return {"where": where, "evidence": "native-hit"}

obs = await_ui(args, [
    hit({"label": "Skip", "action": "press"}),
    hit({"label": "Search", "action": "press"}),
    hit({"role": "textfield", "action": "type"}),
], mode="any")

# Existing target() still requires exactly one matching actionable element.
# Branch on Skip / Search / textfield; dispatch each selected action once.
act(args, obs, target(obs, role="textfield", action="type"),
    "type", text=query)

obs = await_ui(args, [
    {"where": {"role": "textfield", "value": query}, "evidence": "tree"},
    {"where": {"role": "text", "label": query}, "evidence": "tree"},
])
```

The final two conditions preserve the smoke test’s existing claim: this sample’s tree contains its query value and result label. They intentionally do not claim visual visibility. Request `native-hit` explicitly in a separate test when a particular app’s passive text supports exact AX hits; do not silently downgrade a failed native-hit condition to tree presence. `any` exists for the real onboarding alternatives, `all` for the real query-plus-result conjunction; there is no nested boolean expression language. The `action` equality checks an existing supported action name in `Element.Actions`, and exists because the onboarding flow needs an actionable control, not just a label.

The harness adds service-reported attempts to a separately named `waitObservationAttempts` count; measured explicit `observe` timing samples stay separate. Observation errors use the wait diagnostic count rather than counting a failed wait once as one failed observation. The one-shot `has_query`/`has_result` helpers may remain for measured/final observation assertions, but they do not poll or own deadlines.

### Call site 2: MCP

```json
{"jsonrpc":"2.0","id":23,"method":"tools/call","params":{
  "name":"phone_wait","arguments":{
    "device":"EXACT-MANAGED-UDID","lease":"LEASE-TOKEN",
    "conditions":[{"where":{"identifier":"ready-label"},"evidence":"native-hit"}],
    "timeoutMs":5000,"screenshot":true
  }
}}
```

The result carries the same JSON envelope as CLI. On success with a screenshot, existing MCP image handling finds `result.observation.screenshot` and attaches its JPEG. A `notifications/cancelled` message for request ID 23 cancels the HTTP request and the active wait; EOF cancels all outstanding requests. Cancellation is handled by the read loop even when every worker slot is occupied.

### Call site 3: matrix

```json
{"steps":[
  {"action":"press","identifier":"load-button"},
  {"action":"wait","conditions":[
    {"where":{"identifier":"ready-label","label":"Ready"},"evidence":"native-hit"}
  ],"timeoutMs":5000,"screenshot":true},
  {"action":"assert","expect":"Ready"}
]}
```

`action:"wait"` accepts precisely the same condition, mode, timeout, and screenshot contract. It replaces the matrix’s current observation with the successful wait observation, and records timeout diagnostics when it fails. A preceding action is executed once, irrespective of subsequent wait outcome.

Legacy `expect` stays a one-shot exact label-or-string-value **tree-presence** assertion against the current observation. Rename error and report wording accordingly, document the compatibility meaning, and update examples that need asynchronous readiness to use a wait step. It is not converted implicitly into native-hit or delayed matching. Reject an `expect` without a valid current observation and reject unknown step actions rather than accidentally count a no-op as a passed assertion. A matrix with no assertions remains `observed`; a completed explicit tree assertion or successful wait supplies an actual checked condition. The bare first-accessibility startup readiness operation uses the same private wait loop with a private “first successful sample” goal, not a second retry loop or a public empty condition list.

## Shape

### Domain first

All untrusted request fields are parsed once before phone acquisition. Inside the phone package, the poller receives a validated `waitSpec`; it never repeatedly parses JSON or interprets arbitrary callbacks. Existing HTTP/MCP wire structs remain boundary adapters. Native maps never appear in the wait API.

```go
// Private validated domain values; zero values do not reach the loop.
type evidenceKind uint8
const (
    treeEvidenceRequired evidenceKind = iota + 1
    nativeHitRequired
)
type waitMode uint8
const (allConditions waitMode = iota + 1; anyCondition)
type actionName string // existing supported action vocabulary, boundary validated

// nil means unconstrained; pointer-to-empty string means equal to empty string.
// value is a string equality in this first version, not coercion from any JSON value.
type selector struct {
    role, label, identifier, value, context *string
    action *actionName
}
type condition struct { where selector; evidence evidenceKind }
type waitSpec struct {
    conditions []condition // 1..16, each nonempty; explicit valid evidence
    mode waitMode
    timeout time.Duration // 1ms..30s, default applied at boundary
    screenshot bool
}

// Observation remains the public sampled state. Add this to Element:
// NativeHitAt *time.Time `json:"nativeHitAt,omitempty"`
// A missing field means there is no accepted native-hit proof in this sample.
// It never means “invisible.” The timestamp is receipt completion, not an
// assertion that the earlier AX tree and native hit were atomic.

type normalizedCandidate struct {
    element Element
    identity targetIdentity
    canProbe bool             // local, complete, unique semantic identity, bounded depth
    inputActions []actionName // derived from role/enabled/identity, empty for passive text
}
type normalizedSample struct {
    observation Observation
    candidates []normalizedCandidate // same index order as observation.Elements
}
type capturePlan struct {
    indices []int // at most 250 distinct element indexes, one native batch
}
type nativeHitReceipt struct { // parsed behind native transport/evidence boundary
    token string
    identity targetIdentity
    app, context string
    receivedAt time.Time
}
type sampleMatch struct {
    satisfied []int // indexes into original conditions; no arbitrary first action target
}

type WaitResult struct {
    Observation *Observation `json:"observation"`
    Satisfied []int `json:"satisfied"`
    Attempts int `json:"attempts"`
    ElapsedMS int64 `json:"elapsedMs"`
}
type WaitDiagnostics struct {
    Attempts int `json:"attempts"`
    ElapsedMS int64 `json:"elapsedMs"`
    Satisfied []int `json:"satisfied"`
    ObservationErrors int `json:"observationErrors"`
    LastObservationError string `json:"lastObservationError,omitempty"`
}
```

Selector `action` is allowed only with `native-hit`; validating this combination prevents the nonsense of requesting an actionable target with only tree evidence. No negation, disappearance predicate, numeric comparison, regexp, substring match, user code, changed-screen signature, frame counter, or stable-for duration ships here. Numeric or boolean accessibility values remain in observations but do not acquire an implicit string representation for equality. Future concrete callers can justify extending the scalar type.

`normalizedCandidate` separates probe eligibility from input eligibility at creation. A local disabled control can still be probed; it receives no input actions. A passive text/image candidate can receive a native hit without ever obtaining input actions. The old role/enabled/local/completeness/semantic-uniqueness protections remain authoritative for action eligibility. No receipt sets `Actionable=true` by itself. `Actionable` and `Actions` are derived from the intersection of input eligibility and an accepted native receipt, never inferred from the presence of text.

### Signatures and knowledge ownership

```go
// observation.go: bounded UI observations and their evidence form one deep module.
func parseWait(params Params) (waitSpec, error) { panic("not implemented") }
func matchSample(s normalizedSample, q waitSpec) sampleMatch { panic("not implemented") }
func planCapture(s normalizedSample, q *waitSpec) (capturePlan, error) { panic("not implemented") }
func associateReceipts(s *normalizedSample, plan capturePlan, receipts []nativeHitReceipt) error {
    panic("not implemented")
}
func (s *Service) captureSample(ctx context.Context, p *Phone, q *waitSpec) (normalizedSample, error) {
    // TODO: clear current observation; read/decode tree; one native batch;
    // associate accepted receipts; do not publish this sample yet.
    panic("not implemented")
}
func (s *Service) waitUI(ctx context.Context, p *Phone, q waitSpec, started time.Time) (*WaitResult, *WaitDiagnostics, error) {
    // TODO: one deadline loop, sample goal, optional screenshot, final checks, publish.
    panic("not implemented")
}
func (s *Service) awaitFirstSample(ctx context.Context, p *Phone, started time.Time) (*Observation, error) {
    // Private startup goal in the same loop; retries bounded observation failures.
    panic("not implemented")
}

// service.go: one owner of mutable phone state, including all existing methods.
type phoneGate struct { token chan struct{} } // initialized by newPhone
func (g *phoneGate) lock(ctx context.Context) error { panic("not implemented") }
func (g *phoneGate) unlock() { panic("not implemented") }
func (g *phoneGate) tryLock() bool { panic("not implemented") }

// transport.go: context-aware operations on persistent, owned helper streams.
type phoneBridge interface {
    Tree(context.Context) (map[string]any, error)
    Action(context.Context, map[string]any) (map[string]any, error)
    Close() // idempotent; closes streams and kills/reaps its processes once
}
func NewBridge(ctx context.Context, udid, root string) (*Bridge, error) { panic("not implemented") }

// cmd/agent-phone/main.go: preserve context from caller to HTTP.
func client(ctx context.Context) (*http.Client, error) { panic("not implemented") }
func request(ctx context.Context, method string, params json.RawMessage) (phone.Response, error) {
    panic("not implemented")
}
```

The two startup/wait signatures do not mean two engines: an unexported goal discriminator chooses `firstSample` or `conditions` inside the single loop. Avoid a public function-valued predicate that invites callers to reintroduce private polling semantics. This extra private goal exists only to replace the real matrix startup loop; public `wait` rejects an empty condition list.

The module map is intentionally flat:

| File | Owns |
|---|---|
| `internal/phone/service.go` | Managed-device lookup, request admission/deadline start, cancellable per-phone gate, lease validation/renewal and dispatch; all phone writers use this gate. |
| `internal/phone/observation.go` | Validated query types, pure matching, capture plan, receipt association, private sample construction, wait loop and publication; existing selected-target action rules remain alongside their evidence. |
| `internal/phone/transport.go` | Helper lifetime, bounded stream operations, typed observation failure classification, cancellation and stream poisoning. |
| `internal/phone/matrix.go` | Manifest adaptation and honest report vocabulary; calls the shared observation contract, owns no polling. |
| `cmd/agent-phone/main.go` | CLI/MCP schemas and contexts; HTTP cancellation; no condition evaluation. |
| `scripts/smoke-wikipedia.py` | Scenario choices and input once; service wait requests; measured observations and report accounting. |

The control path stays `service.go -> observation.go -> transport.go`. Avoid a scheduler, global wait registry, observation bus, generic condition engine, or a second helper per wait. Query state is local to its request; phone state has one writer under its gate. Different phones remain independent. This satisfies separate-before-serializing-shared-state without splitting the single phone’s inherently coupled tokens and current observation across actors.

### Dominant access patterns

1. **Exact semantic query:** scan at most the existing bounded tree once per sample, recording condition candidates by element index. Sixteen conditions and the existing 20,000-node native cap make the bounded linear scan adequate; no synchronized index or cache is necessary.
2. **Native-hit query on passive text:** union eligible semantic matches for native-hit conditions, then fill remaining slots with ordinary input-eligible controls. Deduplicate by sample index and issue exactly one `capture-targets` request. Its replacement-token semantics therefore cannot invalidate an earlier action-only batch. A query requiring more than 250 distinct probe candidates returns `selector_too_broad`; the caller must add exact role, identifier, or context constraints. It does not silently starve late candidates. Native query candidates take priority; action controls beyond the remaining cap stay non-actionable, as bounded observations already permit.
3. **Query on action capability:** preselect by semantic equalities and potential supported action, then require the accepted receipt and final `Actions` membership. This does not grant actions to passive candidates.
4. **Return for subsequent action:** only the final accepted sample becomes `p.observation` and `p.lastObservation`. Unsuccessful polling samples stay request-local. The previous consumable observation is invalidated on the first capture; a timeout/cancellation leaves no current observation from this wait. A caller that timed out before gaining the gate has not touched phone state. Native action validation still checks the chosen ref/token immediately before input, independently of wait success.
5. **Tree-only query:** its condition needs no extra passive probes. Ordinary action-target capture remains available in the final returned observation. Tree results do not acquire native-hit evidence through bounds checks, role, or filtering.

The capture batch accepts a receipt only when its cardinality and structure are valid and its token, app, context, and exact raw semantic identity agree. An ordinary mismatch is nonmatching evidence, not a fatal service error and not a reason to guess a containing cell is the requested text. Truncated/incomplete/remote/ambiguous evidence cannot manufacture an accepted native witness or input permission. A semantic duplicate that prevents reliable association stays tree-only.

### One deadline, including admission

At `Service.Call` entry record `started := time.Now()`, retaining Go’s monotonic component. After boundary parsing, derive `deadline = min(started + timeout, parent deadline)` before managed-device gate acquisition. No operation renews the deadline. Use a context derived from that deadline and also test `!time.Now().Before(deadline)` directly: a delayed context timer must not permit late success.

Replace the phone mutex with the one cancellable gate for **all** phone operations, reaper access, fleet snapshots, and shutdown cleanup. The gate’s acquisition checks cancellation before waiting and immediately after winning its select; if cancellation and acquisition race, it releases its token and returns an error. Do not implement cancellation by leaving a goroutine blocked in `Mutex.Lock`.

Hold the phone gate for the whole wait, including delays. This deliberately serializes a single phone’s control session while its predicate is pending and prevents another same-lease command from replacing native tokens between sample and publication. The maximum normal occupancy is 30 seconds, and cancellation unblocks it. On admission, validate the current lease at the actual current time, then renew it once using the existing inactivity rule. Check lease validity again before each capture and before publication; polling does not repeatedly extend a lease. A lease expired during queueing cannot be revived by the waiter. Treat equality to expiry as expired.

Loop pseudocode:

```text
check context, monotonic deadline, lease
capture private sample using that context
check context and deadline before examining success
if sample has a classified transient read/startup failure:
    retain bounded diagnostic; retire damaged bridge; wait remaining delay; repeat
if sample has a terminal boundary/protocol/configuration error: fail
evaluate all/any exact conditions against sample evidence
if unsatisfied: retain only compact diagnostics; wait remaining delay; repeat
if screenshot requested: capture one screenshot with same context and timestamp it separately
finish required local bookkeeping; check context, deadline and lease again
publish sample as the current observation and return success without further blocking work
```

The delay uses a timer and `select` on context, bounded by remaining time. No screenshot is attempted after timeout for diagnostics. No unsuccessful sample is accepted because it was captured before the deadline if decoding, required screenshot, or bookkeeping completes afterward. Synchronous event/artifact work precedes the final acceptance check; do not emit a claimed success event before that check. Existing observation events may describe completed samples without certifying a successful wait.

The contract is a **semantic deadline**, not hard real-time delivery. OS process start/kill/reap, memory scheduling, JSON encoding, and filesystem operations can delay error delivery. Such lateness must not produce successful matching. The service starts this budget at request entry; process startup and response transit outside the service are not included in `elapsedMs`. CLI/MCP context cancellation still bounds their client bootstrap/connect work and propagates caller cancellation. Document these two scopes rather than pretending a fresh CLI process and local daemon share one monotonic clock.

### Persistent helper lifetime and cancellation

`Tree(ctx)` and `Action(ctx, request)` apply the earlier of the operation’s remaining deadline and existing per-I/O safety cap to every write/read. Startup uses the same context for reader readiness, `DialContext`, action-helper readiness and `prepare`. Check cancellation immediately before and after noninterruptible process starts. No independent ten-second startup budget or fifteen-second I/O budget may extend an earlier wait deadline.

Healthy helpers outlive successful requests. Do not use request-lifetime `exec.CommandContext` for persistent reader/actions processes. Startup failure owns and closes its partial bridge. A canceled or failed in-flight stream is poisoned: close its descriptors to unblock I/O, kill/reap its processes once, and clear `p.bridge` while the phone owner still holds the gate. The next operation may start a new bridge. There is no unread late response that can be mistaken for a new request.

An operation-local cancellation callback may interrupt its I/O handles, but it does not mutate `Phone`. Stop and join that callback before returning or releasing ownership, so a late callback cannot close a healthy bridge reused by the next request. Cancellation during the poll timer retains an otherwise healthy idle bridge. Transport errors are classified at their boundary: startup/read temporarily unavailable, terminal invalid protocol/configuration, or context termination. The wait retries only the first class. Missing helper binaries, malformed responses, unmanaged devices, invalid leases/selectors, and cancellation are not hidden behind polling. The private first-sample goal uses the identical classification.

Changing the bridge signatures necessarily updates `observe` and `act` callers. For an input whose acknowledgment is lost, consume the observation and return the existing failure/ambiguous outcome; never resend input as transport recovery. The wait module only invokes tree read, capture-targets, and optional screenshot operations.

### Boundary integration

CLI installs a signal context and uses `http.NewRequestWithContext` plus `Client.Do`. MCP registers a cancellation function under a canonical JSON-RPC ID **before** scheduling work, handles cancellation notifications in its input loop, and cancels outstanding work on EOF. Keep the request table bounded (for example 8 active/queued calls), reject overflow rather than block the input reader, and protect duplicate live IDs from cancel-function replacement. Strings and numbers are distinct IDs; cancellation and completion remove only their own entry. These are request-transport mechanics, not a second wait service. A parent context is honored by direct Go and matrix callers too.

Extend the existing request/Step boundary with the same nested condition wire struct and parser. MCP exposes exactly those nested fields with `additionalProperties:false`, evidence/mode enums, and the shared numeric bounds. Keep the explicit existing schema approach, but add schema/parser parity tests so a hand-maintained field or limit discrepancy fails the build. Do not grow a generic schema engine for this one method.

### Interface depth and invariants

One `wait` call hides phone admission, one persistent transport, retry classification, native association, deadline enforcement, poll scheduling, optional image capture, and safe publication. The caller exposes only the semantic equalities, evidence strength, all/any choice, and budget. This is a deeper surface than asking clients to compose readiness, observe, hit-test, screenshot, and timeout calls. Boundary-discipline keeps untrusted maps out of matching; encode-lessons-in-structure separates native proof from possible input actions; single-source-of-truth keeps deadlines, lease state, and token ownership in one place. Pure matching never reads the clock, performs I/O, or changes observation state.

The design has one deliberate non-idempotent side effect in its observation protocol: each native capture replaces action tokens, and each successful wait supersedes previous observation refs. Repeating a wait is safe for app state but yields new evidence and refs. Repeating a timed-out or canceled input is never implied. No additional process survives independently of its phone owner.

## Synthesis decision

Candidate A is proposed as a base for its query-directed capture and single publication boundary. The orchestrator will fill the final synthesis decision after comparing independent candidates. No claim about the other candidate is made here.

## Tradeoffs accepted

- We accept that native-hit can succeed on visually obscured content in exchange for an accurately named, useful AX association fact. A real visual-visibility predicate requires a stronger evidence source and is not implemented under another name.
- We accept holding one phone’s operation gate during a bounded wait in exchange for stable helper/token ownership and a small cancellation model. Other phones and lease-free live vision remain independent.
- We accept targeted passive probing, a 250-point bound, and terminal “selector too broad” errors in exchange for resource predictability without hidden witness starvation or repeated token-reset batches.
- We accept potentially delayed timeout delivery during OS cleanup in exchange for complete cleanup; late success is always rejected.
- We accept only flat all/any string equalities plus existing action capability equality in exchange for removing both known Python polling patterns without creating an expression engine.
- We accept that screenshots illustrate a separately timestamped sample and may show a later state in exchange for preserving honest AX/image provenance.
- We accept legacy `expect` as explicitly named tree-only compatibility in exchange for avoiding an unannounced strengthening of existing manifests. New readiness examples use `wait`.

## Alternatives considered

- **Client-side polling library:** hides the Python sleep loop but exposes helper lifetime, lock admission, transport errors, cancellation, screenshots, and inconsistent matrix/MCP rules to each caller. Its surface is shallow and cannot enforce the required service deadline.
- **Continuous observation producer plus subscriber queries:** hides polling behind a hub and could reduce duplicate reads, but exposes cursor/replay/freshness policy and introduces an observation writer competing with consumable action refs. Lease-free vision already has that distinct model; UI control does not need it.
- **Probe every passive element on every observe:** gives uniform broad evidence but spends scarce batch capacity on irrelevant labels, can hide useful action targets, and makes ordinary observations more expensive. Query-directed capture concentrates necessary evidence in the one existing native batch.
- **One-selector wait only:** a smaller signature exposes repeated budget coordination and local any/all evaluation to the actual Wikipedia callers. The flat two-mode list hides that real coordination without admitting arbitrary expressions.
- **Call native-hit “verified visible”:** rejected by the parent’s opaque-cover live result, irrespective of interface convenience. Frame containment, sibling geometry, or an AX overlay heuristic cannot repair the claim for non-AX overlays.

## Regression and live acceptance plan

These are required checks, not results claimed by this design runner. The parent owns all builds and live simulator work.

1. **Deadline/admission tests:** hold the phone gate beyond a short timeout; assert return without obtaining phone state, starting helpers, or altering the existing observation. Inject a sample that completes at/after deadline with a matching element; reject success even if `ctx.Err()` has not fired. Repeat with screenshot completion and startup/readiness beyond budget. Cover an earlier parent deadline, explicit cancellation, and cancellation racing gate acquisition.
2. **Transport/lifetime tests:** blocked socket read, action pipe read and write, readiness dialing, and prepare all stop under the same budget. Assert damaged streams are closed and no late reply is reused. Healthy helpers are reused across successful waits; cancellation while merely sleeping does not kill them. Partial startup closes both processes. Callback completion cannot close the next request’s bridge. No goroutine or file-descriptor growth over repeated cancellations.
3. **Evidence tests:** tree-only text matches a tree query; native-hit needs an associated receipt. A passive matched receipt adds `nativeHitAt` but no action. Containing-cell/mismatched label, app/context mismatch, missing token, duplicate raw identity, incomplete tree, and remote branch never invent a native witness. A verified button still receives its permitted actions, and unrelated changing labels do not invalidate it. Probe plan contains passive query candidates and action targets in one batch; required union over 250 fails explicitly.
4. **Predicate tests:** empty request/unknown fields/invalid evidence/invalid mode/bad timeout rejected; empty-string equality distinguished from omitted field; string values not coerced; flat any onboarding and all query-plus-result evaluated on one completed sample. No condition accumulates success across different samples. Duplicate matching labels satisfy existence but are not represented as a unique action target. Context equality can narrow a broad query.
5. **Lease and input tests:** invalid and expired leases fail; expiry during gate queueing is not renewed; cancel/timeout does not publish refs. Existing stale/consumed-target tests remain. A native input failure followed by any wait cannot produce a second input call. Waiting uses no press/type/scroll dispatch. State on another phone remains independent.
6. **Integration tests:** CLI and MCP schema expose the same wait contract; unknown nested fields fail consistently. MCP numeric/string ID cancellation, duplicate IDs, EOF and capacity overflow keep cancellation responsive. Matrix wait success/failure preserves evidence/report status, runs prior input once, and legacy `expect` says “tree presence.” Startup uses the same engine and rejects late initial samples. Python removes its retry loop and records service attempt counts accurately.
7. **Resource-conscious validation:** root runs `make test`, including the existing race suite and Python harness, then serialized builds with isolated configuration outputs. No candidate runner builds or boots a phone.
8. **Fixture live checks:** use one exact preexisting managed Wikipedia phone under a current lease. The fixture’s delayed Ready label proves a wait transitions from unmatched to matched within budget while a 100 ms unrelated timer changes. Short timeout and cancellation fail without input replay. Use a fixture case whose AX hit actually returns a different cover element to prove native mismatch remains unsatisfied, if such a case exists; record the observed semantics rather than forcing them. Separately retain the opaque-cover case: tree may pass and native-hit may also pass despite a visually hidden label. Inspect its screenshot and report this as an explicit visibility limitation, never as detection of occlusion. A revealed passive label with a matching receipt remains non-actionable; attempting `act` on it is rejected.
9. **Official live demonstration:** relaunch the unmodified official Wikipedia app, run the migrated smoke flow through CLI and one MCP wait, verify query/result tree evidence and screenshot timestamps, verify helper reuse, release the lease, and restore the phone’s initial Shutdown state. Preserve all other managed phones. If native-hit result text is not supported in Wikipedia, record that limit and keep its query/result claim tree-only.
10. **Documentation:** update README method list/tool count from actual implementation, condition/evidence semantics, matrix wording, cancellation, deadline scope and Python statistics. Record actual test/live outcomes and all skips in the validation document. Preserve upstream licenses/revisions; no native source edit or new upstream import is needed for this candidate.

## Open questions and risks

- Will the current native runtime ever supply enough information for a visual-visibility assertion, or should a future feature require explicit independent image/occlusion evidence? This release answers by exposing no such predicate.
- Does the live Wikipedia tree provide exact local native hits for result labels, or do hits resolve a container? This is a live characterization question; the shipped smoke contract remains tree presence either way.
- Is the bounded gate occupancy acceptable for callers who submit release concurrently with a wait? This design chooses cancellation as the immediate interruption mechanism and retains exclusive ownership until cleanup completes.

None requires a new human approval checkpoint: these are documented limitations or testable runtime questions with explicit conservative behavior.

## Next implementation step

Build the cancellable per-phone admission and context-aware persistent bridge operations with deadline regressions, then implement the query-directed sample/publish loop against those boundaries before exposing CLI/MCP and migrating matrix/Python callers.
