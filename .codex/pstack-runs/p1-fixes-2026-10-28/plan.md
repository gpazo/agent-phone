# P1 fixes

Scope: persistent MCP transport lifetime; matrix target/runtime/type and build artifact isolation plus failed outcome propagation; native capture association and transport recovery.

### Bug fix

**You own this task. Plan, review, verify.** Delegate investigation and the fix to subagents, stay in the lead. Pick each subagent's reasoning level and fast or standard speed for the job, per the Subagents section.

Be scientific. Every shipped line traces to runtime evidence. Belt-and-suspenders that "might help" is a hypothesis, not a fix. It does not ship. When evidence refutes a hypothesis, revert what it motivated. The smallest change the evidence justifies ships, nothing more.

1. Reproduce it yourself on the matching surface through the applicable Codex browser, computer-use, terminal, or project verification skill (Non-negotiables). Don't hand the repro to the user. A debug or instrumentation protocol that says to ask the user does not override this. You drive the instrumented runtime. Ask the user only with a stated, specific reason the real verification surface cannot reach the target, and only after driving it as far as it goes. Won't reproduce directly, force it: synthesize the trigger, tighten conditions, or instrument until it fires.
2. Binary-search the cause. Form the candidate hypotheses, then rule them out until one survives. Seed them with `how` over the affected subsystem and the **why** skill for regression history. Each pass, take the split that cuts the most remaining problem space, get runtime evidence, eliminate. When program state is unclear, add instrumentation or logging and read it as the code runs. Don't guess. Drive a long or stubborn hunt with a Codex heartbeat automation or bounded monitor loop. Confirm the surviving *mechanism* with runtime evidence before the step-3 architect/interrogate fan-out.
3. Plan the fix. If it crosses a function boundary, `architect` first. Delegate implementation to a subagent using your configured bug-fix model (default `gpt-6-luna`) with a specific scope. Review the diff.
4. Verify on the same surface. The original repro now passes. "Inconclusive" or wrong-surface is not a pass. Flag it. Unit tests show branch behavior, not bug absence.
5. Stage the commits so the failing repro lands before the fix in git history. See the **tdd** skill for the failing-test-first cadence when the bug has a cheap local test path. Skip it when the test would be expensive, integration-heavy, or unclear.
   This is the canonical **sequence-verifiable-units** principle skill, the failing test first and the fix on top.
6. Run **Opening a PR**.


**Reply:** what was broken, root cause, fix, how you verified. Paste failing-then-passing repro output verbatim.

## Task decisions

- Existing source review grounds how. No regression history exists because the repository has no commit.
- Preserve the untracked user checkout. Use an ignored snapshot Git repository with isolated worktrees for concurrent writers. Integrate reviewed patches only.
- Reproduce with offline behavior fixtures, then verify CLI/MCP and explicit leased Wikipedia phones. Serialize all native/Xcode builds in root.
- Throughput checkpoint: three independent repair slices; root integrates shared Result/service changes, owns live simulator validation, and serializes builds.
- Data shapes: process-owned RPC client, normalized matrix/build/device identity with a typed aggregate report, observation-scoped semantic identity and capture receipts.
- Architect phases: Ground, Sketch, Agree without human checkpoint, Implement, Scrap only if evidence requires.
- Arena phases: Frame, Fan out two candidates, Cross-judge, Pick, Graft, Verify. Rubric: correctness across boundaries; externally testable behavior; smallest API; preservation of lease/target invariants; build/artifact isolation.
- Commit/PR steps: skip user-checkout commits and PR because there is no initial commit or remote-backed base. Worker snapshot commits preserve red/green evidence without committing user files.

## Completion

- Integrated transport, matrix, and native red/green patches from isolated snapshot worktrees.
- Parent reproduced all original failing fixtures and the live persistent MCP socket leak. Native/transport red logs were retained by rerunning their isolated red commits.
- Fresh Comment Sicko review found no scoped comment issues. Independent correctness review found a case-insensitive APFS alias bypass. Parent reproduced the new red tests; worker fixed native canonical spelling and volume-aware handling of missing path components. Follow-up comment and correctness reviews passed.
- Required make test and make build passed on the combined checkout. Go race checks and five Python tests passed; the case-sensitive-volume fixture skipped on this host.
- Live MCP connections: twelve retained sockets before, one after twelve requests.
- Live matrix validation: runtime/type/output conflicts rejected, CLI/MCP retain failed reports, real A/B build snapshots remain isolated, two variant labels match, deliberate assertion failure shuts down and releases its phone.
- Final live case check: three case-alias shapes rejected before build; uppercase project alias and existing Unicode output path build successfully.
- Four-phone Wikipedia run: first run failed immediate text readback; post-failure observations confirmed text had arrived. A distinct-query run then passed with one recovered observation error. Final fresh observations confirm all four managed phones on Wikipedia and unowned.
- Actual results, evidence paths, filesystem limits, non-atomic capture limits, and transient native read behavior documented in docs/validation-p1-2026-10-28.md and README.md. Source provenance remains pinned; vendored native files unchanged.
