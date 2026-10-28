# Four-simulator demo

User requested pstack and four simulators. Use pstack's Swarm workflow with two read-only exploration workers. Parent owns source edits and live simulator state. There is no configured pstack-models.md; swarm role defaults to gpt-6-luna at xhigh.

Done predicate: four managed Wikipedia simulators concurrently running, each independently searched and verified with its own query, lease and evidence. Repeatable invocation, accurate memory/latency report, four visible dashboard previews, and regression checks. Do not bypass critical memory pressure or alter unmanaged devices.

## Swarm phases

- [x] Frame. Partition into capacity-policy and demo/dashboard coverage. No race.
- [x] Fan out. Two read-only workers, separate reports in chat.
- [x] Aggregate. Inspect each result and implement the selected minimal design.
- [x] Report. Consolidate live results, issues and gaps.

## Work

- [x] Measure current fleet and host pressure; inspect memory gate and startup.
- [x] Generalize the repeatable demo to four named variants and independent queries.
- [x] Make four-phone admission explicit and bounded on this 8 GiB host.
- [x] Run all four simultaneously and verify cross-device isolation.
- [x] Check dashboard, race tests, and evidence documentation.

Two exploration workers completed. A fresh review worker found an unsupported empty-string clear operation and an insufficient clear assertion. Replaced that operation with verified, nonempty preflight input. Also strengthened MCP checks to decode and compare the actual JPEG bytes and verify the returned device. Partial failures were retained in .cache/wikipedia-four-initial-failure.json. Four-phone control run then passed. All source writes and live device state stayed with the parent.

Final make demo-four passed with four setup observations, four independent searches, 1330.05 ms common search overlap, zero smoke observation errors and four byte-verified MCP JPEG responses. Four previews were visually verified in the native dashboard. The final independent review returned PASS. Matrix first-read retries changed from three fixed attempts to a 30-second context after a reproducible startup-read failure. Existing pressure-read failure behavior remains a documented policy gap, not a newly changed guarantee. Final evidence: docs/validation-four-2026-10-28.md.
