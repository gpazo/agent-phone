# Implementation decision

The independent code judge chose A, 23/25, over B, 15/25. The parent read both complete diffs and tests and agrees. A supplies one ownership path, bounded ordinary observations, valid CLI/MCP adapters, and broader behavioral coverage. B has out-of-range live timeout values and malformed nested selector schemas.

Accepted grafts from review are preserving completed AX/native evidence before an optional screenshot, and treating non-object native target receipts as malformed terminal responses. Empty target objects remain ordinary misses.

Runtime evidence corrected the parent's narrowed design. Removing all startup retry behavior made the matrix fail on kAXErrorServerNotFound immediately after launch. Two following probes reported window-server frontmost returned no application object while a screenshot showed the running fixture. Restore the design's private first-complete-observation goal and retry only these explicit reader unavailability responses inside the shared deadline loop. Arbitrary protocol, transport, truncated-tree and helper failures remain terminal. The error classification must be typed at the protocol boundary, not inferred from generic call failures.

A integrated as aa4e135, 4bba368, 6bf32df after fixture 15bff68. Independent candidate commits remain available for comparison. Baseline and failed live artifacts are in ignored .cache/ui-waits/ and .cache/ui-waits-candidate-a-live.json; validation documentation records the failures without treating them as passes.
