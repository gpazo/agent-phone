# Reliable UI waits

### Feature

Pick reasoning effort for the job under the Subagents section of **poteto-mode**, using only the active execution tool’s supported values.

**You own the design. Plan, review, verify.** Delegate implementation. Stay in the lead.

1. `how` over the affected subsystem.
2. `architect` for parallel design exploration.
3. Write the throughput checkpoint as four todo items. A dimension that genuinely does not apply (single file, no fan-out) keeps its item with `n/a: <reason>` rather than being dropped:
   - **Blocking first steps.** Gates run before fan-out.
   - **Independent workstreams.** Disjoint files, services, or layers parallelize. Shared writes serialize.
   - **Shared mutable state.** Default to splitting the target (the **separate-before-serializing-shared-state** principle skill). Serialize only for real invariants.
   - **Smallest safe decomposition.** If one worker is best, name why.
4. Delegate code-writing to a subagent using your configured feature model (default `gpt-6-luna`) with a specific scope (file paths, named data shape and its organizing structure per **principle-model-the-domain**, a state machine over scattered booleans, a table/registry over branching, a typed model over repeated shape assumptions, chosen before the delegate writes logic, and success criteria). Review its diff yourself. When the implementation admits multiple valid shapes (error handling, abstraction layer, test structure), delegate via the **arena** skill instead so the runners surface the alternatives and the cross-judge guards the pick. Mandatory: no skip-with-reason escape, and Laziness Protocol does not override it (the gain is review separation, not lines saved). You can spawn a subagent even though you are one. "The app is small" and "a subagent cannot spawn one" are both wrong. A subagent forbidden to spawn satisfies this by owning the diff directly with the same review separation. No "standing by" reply that waits on a nested agent. Comments per **Comments**. Surgical edits, re-ground against the source for upstream-derived files. Port shared-primitive improvements to all consumers and verify each. Commit liberally.
5. Verify on the matching surface. "Inconclusive" or wrong-surface is not a pass. Flag it.
6. Rebase into small, ordered commits. Stack follow-ups.
   Use the **sequence-verifiable-units** principle skill, building, verifying, and committing each small unit before the next.
7. If the design is contested, `interrogate` before shipping.
8. Run **Opening a PR**.

Code-coupled work (one feature, one migration) goes to a single owner with the checkpoint inline. That owner fans out internally after the blocking phase. Parent-level fan-out is for slices that produce independent artifacts (audits, cross-subsystem investigations, competing experiments). Rewrite the checkpoint at phase boundaries. Spawn a fresh owner rather than chaining interrupts.

**Reply:** what you built, what you chose and why, the throughput checkpoint, open decisions. Tables for design alternatives.

## Scope and decisions

User selected reliable UI waits: deadline-based observation predicates and a distinction between tree presence and verified visibility. Existing native validation, leases, bounded resources, and no input replay remain invariants. No global stability promise.

Architect phases: Ground, Sketch, Agree (no human checkpoint requested), Implement, Scrap only if runtime evidence requires. Arena phases: Frame, Fan out, Cross-judge, Pick, Graft, Verify.

Data shape before logic: a typed observation predicate, explicit evidence requirement, bounded wait outcome with the last observation and diagnostics. Exact public schema is chosen after two designs.

Throughput checkpoint:
- Blocking first steps: inspect source, explain ownership, compare two designs, settle contract and deadline semantics before code.
- Independent workstreams: isolated competing implementations; parent owns live fixture and simulator verification and documentation integration.
- Shared mutable state: worktrees isolate writers. Existing per-phone ownership and persistent helpers need serialized access. All Xcode builds run serially in parent.
- Smallest safe decomposition: one implementation owner per candidate because transport cancellation, observation evidence, API and matrix integration share one contract. Read-only review is independent.

Verification includes fake native transports, timeout while queued/reading, lease failures, no input dispatch, covered elements, passive labels, CLI/MCP failures, a deterministic local fixture and the official Wikipedia app on one explicitly leased managed phone. Restore initially shut-down phone after live checks.

Repository starts clean at a220b19 on main, already pushed. Use codex/ui-waits branch for integrated work. No configured pstack model overrides; use installed role defaults. Model panel shares one provider family; do not call it cross-provider review.

## Progress

- Grounding and design arena complete. Independent judge chose A; parent agreed. See design-synthesis.md.
- Implementation arena complete. Independent judge and parent selected A; see code-synthesis.md for scores, grafts and the runtime correction. Parent owns live harness/docs and fixture.
- Fixture compiled through the service build lock, and baseline make test passed. Fixture committed as 15bff68.
- Forge resolved to gh. Origin CLI is absent and no host tool exposes PR creation. Remote verified as gpazo/agent-phone, default branch main.
- Current transcript for trail review: /Users/gp/.codex/sessions/2026/10/28/rollout-2026-10-28T08-10-43-01a18310-4256-7f32-b04f-c65089fdca2d.jsonl. Review only this task's UI-waits portion.

- Integration ec617bf passed make test with Go race tests and 9 Python tests. make app passed. Final live checks and fresh comment/correctness reviews are in progress.

## Completed verification

Implementation, live validation, Comment Sicko, full correctness review, targeted shutdown review, and trail audit are complete. The isolated candidate workstreams and serialized native builds kept mutable state separate. Native-hit evidence remains distinct from visual visibility, and the final API uses one all/any wait contract.

The selected wait implementation passed one live managed-phone fixture/MCP/matrix/Wikipedia run. The shutdown correction passed a red/green subprocess regression, the complete test suite, make app, and built-CLI idle/backpressure signal checks. All managed phones are shut down and unowned. No open implementation decisions remain. Publication uses the already resolved gh forge; the PR is attached to the Codex task. Candidate archive attempts were refused by app pin protection, so those worktrees are retained.
