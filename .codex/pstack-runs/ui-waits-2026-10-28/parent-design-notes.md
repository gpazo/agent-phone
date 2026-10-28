# Parent design review

Candidate B has a clear shared operation and truthful native evidence names. Its selector example cannot express the current smoke requirement that a query field and a result label both exist in the same observation. Two selectors that are ORed do not meet that requirement. Synthesis should support bounded flat `all` or `any` matching without recursive expressions.

Timeout must be a failed service response with a typed partial result, so CLI exits nonzero and MCP reports `isError`. It is an expected outcome but must not look like readiness success to an agent.

No helper read or target-capture failure should silently become another poll. An unsuccessful predicate is retryable; loss of observation integrity is an error. Healthy persistent helpers survive successful waits. Canceled I/O discards its stream before a later caller obtains ownership.

An observation returned on timeout is diagnostic. The contract must state whether it can supply live action references. If a later in-flight sample invalidated those references, never suggest otherwise.
