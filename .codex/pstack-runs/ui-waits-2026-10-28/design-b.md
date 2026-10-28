# Candidate B: one deadline-bound wait operation

## Problem

CLI, MCP, matrix setup, and the Wikipedia smoke client currently observe and poll in different ways. Their meanings also diverge: a matrix `expect` passes when a matching node is in the accessibility tree, even if an opaque overlay covers it. A parent-run WaitFixture confirmed that both the underlying “Covered evidence” label and the opaque cover appear in the tree; the native center capture also returned the covered label’s token while the screenshot showed it fully obscured. So neither tree presence nor a native AX center hit can honestly mean pixel visibility. The design needs one bounded observation operation, explicit evidence names, and no input path.

## Usage (caller's view)

The CLI and MCP tool use the same `wait` object. Selectors are positive string equalities: fields on one selector are ANDed; entries in `anyOf` are ORed.

```sh
agent-phone call wait '{
  "device":"EXACT_MANAGED_UDID",
  "lease":"LEASE_TOKEN",
  "wait":{
    "anyOf":[{"role":"text","label":"Ready"}],
    "evidence":"treePresence",
    "timeoutMs":30000,
    "screenshot":true
  }
}'
```

The MCP call is `phone_wait` with those same `device`, `lease`, and `wait` arguments. A match returns an observation and references to the matching elements. A timeout returns `outcome: "timedOut"` and the last complete in-budget observation, if one exists. Cancellation, an invalid lease, or a helper error is an error; it is not converted into “still waiting.”

The smoke client replaces its Python predicate callback with declarative selectors:

```python
ready = call('wait', dict(args, wait={
    'anyOf': [
        {'role': 'button', 'label': 'Skip'},
        {'role': 'button', 'label': 'Search'},
        {'role': 'textfield'},
    ],
    'evidence': 'treePresence',
    'timeoutMs': 30_000,
}))
obs = ready['observation']
```

A query/result pair is two positive equalities in one selector, not a client callback:

```python
result = call('wait', dict(args, wait={
    'anyOf': [{'role': 'text', 'label': query}],
    'evidence': 'treePresence',
    'timeoutMs': 30_000,
}))
```

Matrix uses the identical contract as a `wait` step:

```json
{"action":"wait","wait":{"anyOf":[{"role":"text","label":"Ready"}],"evidence":"treePresence","timeoutMs":30000,"screenshot":true}}
```

A caller that specifically wants a native AX center receipt can request `nativeAXCenterHit`. That means the helper returned the same app, context, and semantic element for the selected center point. It still does not mean the text is exposed to the user: the covered-label fixture demonstrates that an opaque, non-AX overlay can leave the underlying label’s native hit intact. There is deliberately no `visible` evidence value.

## Shape

The public request shape is small and closed. Match data is domain data; it does not expose helper messages, native tokens, screenshots as predicates, or an expression language.

```go
type Selector struct {
    Role       *string `json:"role,omitempty"`
    Label      *string `json:"label,omitempty"`
    Identifier *string `json:"identifier,omitempty"`
    Value      *string `json:"value,omitempty"`
}

type WaitEvidence string
const (
    TreePresence     WaitEvidence = "treePresence"
    NativeAXCenterHit WaitEvidence = "nativeAXCenterHit"
)

type WaitSpec struct {
    AnyOf      []Selector   `json:"anyOf"`      // 1..16; fields in one selector are ANDed
    Evidence   WaitEvidence `json:"evidence"`   // required; never defaults to “visible”
    TimeoutMS  int          `json:"timeoutMs"`  // required, 1..30000
    Screenshot bool         `json:"screenshot,omitempty"`
}

type WaitOutcome string
const (
    WaitMatched  WaitOutcome = "matched"
    WaitTimedOut WaitOutcome = "timedOut"
)

type WaitMatch struct {
    Selector   Selector     `json:"selector"`
    ElementRef string       `json:"elementRef"` // resolves in Observation below
    Evidence   WaitEvidence `json:"evidence"`
}

type WaitResult struct {
    Outcome     WaitOutcome `json:"outcome"`
    CheckedAt   time.Time   `json:"checkedAt"` // after reads and any requested screenshot
    Observation *Observation `json:"observation,omitempty"`
    Matches     []WaitMatch `json:"matches,omitempty"`
}
```

Add `Wait *WaitSpec` to `Params`, `Step`, and the strict MCP input schema. The method map gains `wait` / `phone_wait`; `Service.Call` routes it before the ordinary per-call lock path so `wait` can acquire the phone lock repeatedly without holding it during poll delays:

```go
func (s *Service) wait(ctx context.Context, device, lease string, spec WaitSpec) (*WaitResult, error)
func validateWait(spec WaitSpec) error
func matchingElements(o *Observation, selectors []Selector) []WaitMatch
func (s *Service) observeForWait(ctx context.Context, p *Phone, spec WaitSpec) (*Observation, error)
```

`matchingElements` is pure. Boundary validation requires at least one equality per selector, rejects unknown fields/evidence, enforces the timeout and selector count, and bounds strings. Within each selector, every present field must match exactly; a non-string value does not match `Value`. No negation, substring, regex, stability window, or caller-supplied callback is supported.

Replace `Phone.mu sync.Mutex` with a zero-value-safe per-phone serial lock that preserves `Lock`/`Unlock` for existing calls and adds `LockContext(ctx)`. The lock is a buffered one-token channel initialized with `sync.Once`; wait samples use the context form. This makes lock acquisition cancellable without detached lock goroutines. Keep the lock as the single owner of lease, bridge, and current observation state.

`wait` records its monotonic deadline at method entry, before phone lookup and lock acquisition, then takes the earlier of that deadline and the caller context deadline. Each iteration checks cancellation and the deadline, acquires the phone lock with that context, verifies the exact managed UDID and current lease, renews the lease once at admission under existing service policy, and performs one observation sample. It releases the lock before sleeping. Between samples another operation may run; the next sample observes its result. Only a successful sample whose full work has finished before the deadline may match. A final clock check after matching and optional screenshot rejects work that completed late. Poll timers are clipped to the remaining budget.

Pass the wait context through `NewBridge`, `Tree`, and `Action`; their stream deadlines are bounded by the remaining request budget. Cancellation must interrupt in-flight I/O. A canceled or transport-failed stream is closed and reaped while the phone lock is owned, so a late helper reply cannot become the next sample. Successful persistent helpers stay alive after the wait context ends. Screenshot capture runs with the same context and counts against the deadline. Capture it only for a matching sample when requested; if it finishes late, return `timedOut`. `CapturedAt` and `ScreenshotAt` remain separate timestamps, since the AX tree and image are sampled separately.

For `treePresence`, a sample matches when at least one selected element is in that observation’s decoded AX tree. Passive text and image nodes are eligible. For `nativeAXCenterHit`, `observeForWait` adds selected passive nodes’ center points to the existing bounded `capture-targets` batch and requires the returned app, context, and semantic identity to match. It discards the receipt token for passive nodes and leaves `Element.Actionable` false. The separate actionability rules remain: role support, complete and unambiguous tree evidence, app/context/identity agreement, and a retained token. A center hit is only native AX hit evidence; neither evidence value promises pixels, lack of occlusion, full visibility, or screen-wide stability.

The MCP worker needs a request-scoped context registry. `notifications/cancelled` cancels the matching tool-call context; that context flows through the local HTTP request into `Service.Call`. The CLI HTTP request likewise inherits its caller connection lifetime. This is required for cancellation to reach the wait loop and active helper I/O rather than merely stop the client from reading.

Matrix adds an `action: "wait"` step which passes its `WaitSpec` unchanged to `Service.wait`. Keep legacy `action: "assert"` plus `expect` as an immediate assertion against one observation’s accessibility-tree label or string value. Its failure becomes “accessibility tree did not contain …”; docs call it tree presence, not visible text. The matrix’s existing pre-step loop retries any observation error for 30 seconds without checking a UI predicate, so remove that broad retry and perform one observation after launch. Callers declare content readiness with a `wait` step. This keeps snapshot assertion and deadline wait semantics distinct while preserving existing manifests truthfully.

## Synthesis decision

Candidate B only. The parent orchestrator will choose a base and fill this section after cross-judging candidates.

## Tradeoffs accepted

- We accept two clearly named evidence values, neither named “visible,” in exchange for giving callers the exact strength of evidence the simulator backend can support.
- We accept an `anyOf` list of flat equality selectors in exchange for migrating current Python predicates without exposing a general expression engine.
- We accept a dedicated context-aware per-phone lock in exchange for cancellation that includes lock contention and for keeping lease, helper, and observation ownership serialized.
- We accept a `timedOut` result with the last complete observation in exchange for separating an ordinary unsatisfied predicate from cancellation, lease rejection, and helper failure.
- We accept that a requested screenshot is taken only after a candidate match and may cause that match to time out, in exchange for counting image capture inside the budget without paying for screenshots on every poll.
- We accept that matrix `expect` remains an immediate tree assertion for compatibility, while asynchronous readiness moves to one explicit `wait` step.

## Alternatives considered

- A `visible` flag backed by native center capture has a small interface but hides a false assumption: the live opaque-overlay fixture returns the covered element’s native token. It would let callers make a false success assertion, so this design does not expose it.
- Client-side polling with callbacks keeps the service API small but exposes timing, retry classification, cancellation, and observation evidence to every client. The current Python callback already retries broad `RuntimeError` values and accepts a predicate before checking its deadline. It loses on interface depth and duplicated semantics.
- A recursive predicate tree with `all`, `any`, and `not` nodes could represent every client predicate but exposes a query language and its validation rules. Flat selectors with AND across exact fields and OR across alternatives cover the current callers and are easier to validate.

## Open questions and risks

- Should `nativeAXCenterHit` be public in the first release, given that its honest meaning is diagnostic native target resolution and not user-visible exposure? Keeping it supports passive-node inspection; removing it makes `treePresence` the only wait evidence and leaves native probing internal.
- Should legacy matrix `expect` remain indefinitely or be deprecated after manifests have migrated to explicit `wait` and `assert` steps? The proposed first change preserves the JSON shape and corrects its meaning and error text.

## Regression and live acceptance plan

Regression coverage should prove that a late label never changes `timedOut` into `matched`; the budget includes a blocked phone lock, bridge startup, tree read, target capture, optional screenshot, and poll delay; cancellation while queued or reading returns promptly and leaves no stale helper reply; releasing or replacing a lease between samples fails; successful waits reuse healthy persistent helpers; canceled/failed helper I/O is reaped; and a wait never dispatches an input action or retains a passive hit token. Selector tests cover AND/OR equality behavior, invalid/empty selectors, and non-string values. The WaitFixture overlay case must assert that tree presence and even a native AX center hit are not reported as visibility, while a passive node remains non-actionable. Verify that the unrelated 100 ms timer does not block a delayed `Ready` match.

Live acceptance should run the existing WaitFixture on one explicit managed UDID under a lease: wait for delayed `Ready`, inspect delayed reveal with separately timestamped AX and screenshot samples, then reproduce the covered-label case and confirm the result reports only tree/native-AX evidence. Run a wait whose target appears after its deadline and confirm it remains timed out. Finally exercise the same selector through CLI, MCP cancellation, and a matrix `wait` step, then run the Wikipedia smoke flow using the shared wait call. The parent owns builds and simulator operations; this candidate specifies acceptance only.

## Next implementation step

Add the domain types and `Service.wait` around a context-aware per-phone serial lock, then route one regression fake bridge through deadline, cancellation, passive-hit, and overlay cases before migrating MCP, matrix, and Python callers.
