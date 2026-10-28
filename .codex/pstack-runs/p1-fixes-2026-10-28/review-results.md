# Review results

Comment review of the combined P1 diff: no findings, no proposed deletions, no MUST KILL flags. A follow-up review of case-path files also found no issues; its build directive is functional.

Independent correctness review: one P2 gap within matrix output isolation. EvalSymlinks preserves case aliases on this APFS volume, allowing conflicting paths to bypass claims and project cache identity to diverge. Parent reproduced existing/missing/nested alias failures and differing project keys before the follow-up fix.

Follow-up: F_GETPATH supplies existing native path spelling; Fpathconf reports whether missing ASCII components need case folding. Missing non-ASCII paths fail explicitly on insensitive volumes; existing Unicode paths work. Tests and real CLI checks passed. The reviewer's direct filesystem checks confirmed the mechanism and the finding's resolution. No remaining actionable findings in the scoped changes. Case-sensitive-volume preservation was not exercised on this host.

Native semantic replacement between independent captures remains an accepted and documented limitation. Native Objective-C source is unchanged. Live readback and observation transients are recorded in the validation report rather than hidden by retries of input.
