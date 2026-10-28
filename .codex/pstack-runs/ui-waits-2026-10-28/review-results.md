# Review and evidence

The design and implementation arenas both selected A. The parent read both complete code diffs and tests, and accepted the code judge's two targeted grafts. code-synthesis.md records the runtime correction to startup handling.

A fresh Comment Sicko pass recommended deleting only the private phoneGate narration comment. The parent applied it. The reviewer retained two external-protocol comments, checking framed-response consumption and the bundled Go context.AfterFunc contract. No code-refactor flag was justified.

Final cross-model correctness and trail review is in progress. Its verdict will be recorded before publication.

The parent checked this turn's transcript beginning 2026-10-28T18:13:26Z against the decision trail. The design correction, failed first live run, successful integrated run, and final cleanup state have direct local artifacts. The failed run remains a failure; the later pass does not erase it. Candidate archive attempts were refused because the task/workspace is pinned, so both checkouts remain recoverable and unchanged.

Verification details and limits are in docs/validation-ui-waits-2026-10-28.md. The final evidence files were copied unchanged from service screenshots after inspection. Runtime results establish accessibility evidence, never visual-visibility predicates.

The fresh Luna correctness reviewer confirmed an introduced P2 lifecycle defect. An idle MCP process remained blocked on its open stdin after SIGTERM because NotifyContext suppressed the default signal exit. The parent reproduced it with a real CLI child and then killed that child. A fresh owner is fixing input ownership and adding a signal regression; this issue remains open until verification.

The reviewer found no other confirmed code defects. Its score-consistency concern was resolved by checking the two separate arenas. Design B scored 19/25 (5+4+2+4+4); implementation B scored 15/25. Both were correct. implementation-task.md now explicitly calls its numbers design scores. No decision-log row was rewritten.

Audit correction: the initial direct probe's `hasToken` boolean compared an untyped map value with the empty string, so it did not independently prove token presence. Its label/app/context evidence was valid. The final live wait is stronger evidence: the integrated service requires a nonempty string token before setting nativeHitAt, and the covered label received nativeHitAt in .cache/ui-waits-final-live.json. The visibility conclusion is unchanged. Claims about the baseline token should be read with this correction.

An attempted graceful signal fix closed stdin/stdout from another goroutine, but the subprocess regression still hung on macOS. The parent rejected a larger pollable-stdio adapter. MCP did not need to intercept termination signals for this feature; the final design restores ordinary OS signal termination and preserves request notifications and EOF cancellation. A subprocess test will verify pending service work is canceled through socket closure.

## Final disposition

The SIGTERM P2 is resolved by 80a8638. The new subprocess regression failed on the previous implementation, passed after the signal-interception removal, and proved that closing the process cancels its pending Unix HTTP request. The parent combined the owner's red-test and fix commits into one passing unit before publication.

After the fix, make test passed the Go race suite and all nine Python tests, and make app rebuilt the CLI, helpers, and dashboard. The parent tested the actual rebuilt CLI with a valid ping and open stdio, both idle and with blocked output. Both exited with SIGTERM within the two-second bound. Results are in .cache/ui-waits/mcp-signal-after.json. The full simulator run preceded this process-only fix; the shared wait implementation did not change afterward.

Fresh Astra delta review found no additional code defects and confirmed that notification cancellation, EOF cleanup, worker joining, and lease ownership are unchanged. The earlier Luna full review found no other code defects. All review flags are resolved. Remaining limitations are explicit in the validation document: evidence is not pixel visibility, one phone/runtime was exercised for this feature, and candidate worktrees remain because the app refused to archive pinned workspaces.

The final trail audit reconciled the separate design/code scores and corrected the weak baseline token assertion using the stronger final service evidence. No score or decision-log history was silently rewritten. The transcript, retained local reports, committed fixture/verifier, and screenshots support the final claims.
