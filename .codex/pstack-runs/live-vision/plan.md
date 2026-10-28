# Live vision

User requests pstack to solve snapshot-only vision and a non-streaming dashboard first. Task-long pstack mode applies. No pstack-models.md is configured; use validated skill role defaults.

Done predicate: four running managed simulators provide live dashboard frames and agent-accessible current/change frames without stealing a control lease or invalidating action references. Capture is bounded in memory/work and stops when unused. Prove visible UI changes from concurrent native actions, cleanup, backpressure, lifecycle isolation, and resource impact. Deliver working source, repeatable verification, actual results, and limitations.

## Feature playbook

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


## Phase status

- [x] Ground the existing observation/ownership path with how.
- [x] Architect. Ground, Sketch, Agree, Implement, Scrap if necessary.
- [x] Arena design. Frame, Fan out, Cross-judge, Pick, Graft, Verify.
- [x] Throughput checkpoint.
  - Blocking first steps. Validate a persistent Simulator capture source before writing the production transport/UI.
  - Independent workstreams. Read-only grounding and separate design artifacts may run in parallel. Implementation candidates must have isolated source copies.
  - Shared mutable state. Parent alone touches live simulator sessions and running service. Readers must not share action-reference state with control.
  - Smallest safe decomposition. One implementation owner integrates the capture/transport/dashboard contract. Parent verifies live behavior and reviews.
- [x] Implement selected design with delegated owner and independent review.
- [x] Run race/behavior checks and four-device live verification.
- [x] Review source and docs. Record any skipped shipping steps with cause.

## Architecture constraints

The original repo has no HEAD and existing project files are untracked. Preserve them. Design candidates write only separate report directories. Do not manipulate unmanaged simulators. No global tool installs. No arbitrary coordinate action fallback. Keep native AX observations independent of video frames.

Native-source gate passed for all four phones; see probe-results.md. Implementation arena is isolated under /tmp/arena-agent-phone-live-vision/{candidate-a,candidate-b}; baseline is preserved there. Parent serializes all native builds and live operations.
