# Parent-owned candidate build checks

Both isolated implementation candidates passed macOS native helper compilation and SwiftUI app compilation/signing. Candidate B initially had a missing Objective-C @ prefix; its owner fixed it and the rerun passed. These builds were serialized by the parent.

Parent ran each compiled helper against managed device E9008971-4EC6-4654-AA32-11E2AD84D7A0 for three seconds. A emitted nine frames, B ten; all decoded records reported 402 × 874 JPEGs. Closing helper stdin produced clean exit status 0 for both. Capture logs, first JPEGs and raw framing are under `.cache/live-vision-validation/native-{a,b}.*`. No candidate daemon or dashboard has been launched yet; only the parent has operated live devices.

Both owners are completing fake-source Go race/lifecycle tests and docs before code cross-judging. The implementation arena is still open; compile checks do not select a winner.
