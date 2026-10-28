# Final review and shipping disposition

Fresh Comment Sicko review recommended two comment deletions: native source narration and an internal hub ownership summary. Both accepted. Legal/provenance and the Next API contract remain. No MUST KILL flags, suppressions, restored comments, reruns, constraint encodings, or open comment work.

Fresh final correctness review found P2: the timeout started after subscription could wait on old-worker retirement. Parent reproduced it with TestWaitFrameDeadlineIncludesRetiringWorker, moved the deadline before subscription, and ensured deadline-driven automatic subscription cleanup cannot turn a timeout into EOF. Regression tests passed with the race detector; the reviewer repeated both 20 times and confirmed no remaining findings. Full make test and make app passed after the fix.

Live proof: four simultaneous searches and 60 seconds of sustained fresh feeds; independent native actions, MCP image delivery, slow reader disconnection, stalled/crashed capture recovery, leased reboot, service restart, UI reconnection, and zero idle capture helpers. Raw evidence and limits are linked in docs/validation-live-vision-2026-10-28.md.

Principles that changed the implementation: Model the Domain produced explicit frame cursors/epochs and a producer-owning hub. Separate Before Serializing Shared State kept vision outside Phone.mu, AX observation state, and the control connection pool. Prove It Works required real four-phone JPEG/AX validation and fault injection in addition to race tests. Explain the Number kept sampling bounds and measured resource limits explicit.

No commit/PR: this checkout has no HEAD or remote and the entire pre-existing project is untracked. Preserve it instead of creating an unrelated baseline commit. No user permission is needed to complete the requested local feature. Physical-device support, long soaks, and other Xcode versions remain outside this change.
