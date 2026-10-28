# Parent regression probe

Candidate B failed a targeted timeout test in an independent verification copy (`/tmp/arena-agent-phone-live-vision/verify-b/internal/phone/cursor_timeout_probe_test.go`). The completed candidate source itself was not changed.

Trigger: a stream retains a frame from an older epoch while reconnecting; `wait_frame` asks after a different epoch with a 10ms timeout. `FrameSubscription.Wait` immediately returns the retained frame because its cursor differs. `frameCall` rejects the non-live frame but calls Wait again with the same cursor, so it spins without reaching the context branch. The parent test failed after 150ms: `wait_frame ignored 10ms timeout with retained reconnecting frame and differing epoch`.

Candidate A's Wait explicitly checks cancellation and only accepts a live frame before deciding to return. It also has timeout/reconnect tests with a differing epoch. This materially favors A's single-owner lifecycle boundary, subject to its completed check results and the independent cross-judge.

Both final native/Swift builds passed. Candidate B's updated timeline-based age display and bounded child termination were recompiled successfully.
