# Current architecture

The how explainer examined a220b19 read-only.

## Overview

Agent-phone provides serialized observations and verified actions per managed phone, but no shared semantic UI-wait API. Matrix assertions inspect one observation; the Wikipedia smoke script implements its own polling. Reliable waits belong in the Go phone service so CLI, MCP, matrix, and smoke callers share timing and evidence rules.

Actionable controls receive native hit-test evidence. Passive text merely appears in a filtered accessibility tree. Calling every matching label visible text overstates the current evidence.

## Key concepts and flow

Service.Call resolves exact managed UDID, locks Phone.mu, expires stale lease, validates lease, extends it 30 minutes, and dispatches while holding the lock. Phone.mu owns lease, persistent bridge, current consumable observation, last observation. Different phones run independently. Vision frame/wait_frame use a separate lease-free path.

observe invalidates old observation, lazily creates persistent reader/actions helpers, reads native tree, timestamps AX, decodes tree and offers up to250 actionable center points to capture-targets. Matching raw semantics, app, context and token produce actionable controls. Static text/image nodes are not hit-tested. Bounds and in-tree status do not prove unobscured visibility. Tree normalizer filters bounds, derives context, detects full-tree duplicate semantic identity, suppresses actionability on incomplete trees/remote branches.

act consumes the observation, validates native selected target identity/ancestry and live center hit, dispatches one action whose helper repeats validation, sleeps180ms then observes once. Unrelated sibling labels are not compared. No automatic replay; post-action ack does not establish task success.

Matrix startup retries observations for nominal30s at500ms. Step.expect is one exact label/value lookup despite error wording expected visible text. Python smoke wait_for polls300ms and broadly retries RuntimeError; it tests predicate before checking expiration, so a late success can be accepted.

MCP tool fields are explicitly enumerated. Per-message goroutines call an HTTP request using Post without caller context. Notifications without IDs are ignored, so cancellation notifications do nothing. HTTP outer budgets are30min.

## Files

- internal/phone/service.go: admission and locks.
- internal/phone/observation.go: evidence, current references, verified input.
- internal/phone/transport.go: helper startup and streams.
- internal/phone/matrix.go: startup polling, assert steps.
- cmd/agent-phone/main.go: CLI, HTTP, MCP dispatch and schemas.
- native/SimulatorTargets.m: capture-targets accepts any local AX hit, returns token, semantic tree, app/context. It is not restricted to actionable roles; host can probe passive text without native edits. Each capture replaces previous tokens.
- scripts/smoke-wikipedia.py: predicate polling and current official live test.

## Deadline gotchas

Phone.mu.Lock ignores context. Startup reader uses exec.Command, independent10s connect budget and200ms DialTimeout. Native prepare adds15s. Tree/Action ignore context and use fixed15s stream deadlines. Wait's budget must start before phone lock, apply during startup/read/capture and screenshots, and reject late success. Keep healthy persistent helpers beyond request context; do not use a startup CommandContext that kills them when a successful wait ends. On canceled/failed I/O close the helper under ownership to avoid late replies. Process start/kill/reap, decoding and event file writes still prevent a hard real-time scheduling guarantee.

## Semantics and upstream

Native hit verification proves an AX center hit at sampling time. It cannot promise pixel readability, full visibility, or atomic state. Passive text hit may resolve a containing cell; exact mismatch stays unverified. Semantically identical replacement between tree and receipt remains unobservable. Tree presence cannot silently satisfy verified visibility.

Jev's Sources/vphone-cli/VPhoneJevAgent.swift observeSettled uses monotonic elapsed time, bounded polling, consecutive stable samples. Whole-screen signatures are agent policy and unnecessary for selected-predicate waits; unrelated timer changes must not prevent readiness.

## Live state

Four registered Wikipedia phones exist but all start Shutdown and unowned. Parent may boot one explicit managed ID under lease for fixture and Wikipedia checks, then restore Shutdown. Preserve all other phones. No worker may run Xcode, simulator or helper commands; root owns live operations and serialized builds.

## New runtime constraint

The parent built the deterministic WaitFixture and reproduced a covered label passing the legacy matrix assertion. A screenshot proves the label is fully covered by an opaque UILabel. A leased direct capture-targets probe at its center still returns covered-evidence with a native token. Accessibility traversal can choose the covered node. Native AX hit identity is therefore not a proof of visual visibility. Do not weaken this fixture or add speculative overlap heuristics. Name the available evidence tree or native-hit, reject unsupported visible claims, and document that visual visibility remains unverified. Waits may test positive native evidence, not pixel occlusion. Tests must preserve this counterexample and never grant an input action to passive text. The design scope is corrected on runtime evidence.

Audit correction: the initial direct probe's `hasToken` boolean compared an untyped map value with the empty string, so it did not independently prove token presence. Its label/app/context evidence was valid. The final live wait is stronger evidence: the integrated service requires a nonempty string token before setting nativeHitAt, and the covered label received nativeHitAt in .cache/ui-waits-final-live.json. The visibility conclusion is unchanged. Claims about the baseline token should be read with this correction.
