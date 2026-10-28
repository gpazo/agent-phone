# Implementation arena decision

Two isolated writers completed the full feature and passed make test plus parent-serialized native/Swift builds. A fresh read-only cross-judge reviewed frozen candidates. Parent inspected implementation and independently reproduced B’s reconnect-timeout spin in a disposable verification copy.

| Criterion | A | B |
|---|---:|---:|
| Complete integration | 4 | 4 |
| Lifecycle and identity | 5 | 2 |
| Bounded backpressure | 5 | 4 |
| Maintainability and API | 4 | 4 |
| Meaningful verification | 5 | 3 |
| Total | 23 | 17 |

Parent agrees with the judge: choose A. Its independent frame hub owns producer lifetime, pauses during device lifecycle operations, joins retired workers before replacement, and checks cancellation before accepting cursors. B spins past a wait_frame deadline with a retained reconnecting frame and a mismatched epoch; its shutdown boundary is also weaker. This is evidence for the choice, not an unverified performance comparison.

Graft the half-second age refresh and amber stale indicator, keeping A’s dashboard mailbox and lifecycle. Avoid B’s duplicate state machine and extra helper override. Preserve idb licensing and pinned-source provenance.

Original files changed concurrently during the isolated arena: P1 fixes improved build isolation, native identity validation, and shared RPC connection reuse. Parent used three-way merges and preserved every P1 change. The one README conflict retained both the new vision limits and P1 native input limitation. A’s per-request idle-connection cleanup was omitted because it conflicts with the newer bounded persistent RPC pool. B’s proposed test is consequently superseded by existing sequential reuse/concurrency tests. Watch clones the shared HTTP transport into a separate pool and leaves RPC timeouts untouched.

The pre-integration versions are preserved at /tmp/arena-agent-phone-live-vision/pre-integration. Parent-owned live verification remains required. The review panel uses the skill’s default judge model; model-family diversity is limited. No commits or PR: repository has no HEAD or remote and its existing implementation is untracked.
