# Fresh-eyes review

User request: examine agent-phone using ../simslim and ../jev-vphone-cli for experience, codebase, developer UX, and testing improvements. Use pstack for rigor.

Scope: read-only product review. No source changes, simulator mutations, commits, or PRs. Working tree is entirely untracked and is preserved. Review notes and test evidence live here.

## Investigation playbook

1. Route through the **how** skill. For motivation questions, also route through the **why** skill.
2. Throughput checkpoint stays one line: `throughput checkpoint: n/a, read-only investigation`.
3. Produce the `how`-shaped output (Overview / Key Concepts / How It Works / Where Things Live / Gotchas), or a recommendation with a tradeoffs table if the request is a decision between alternatives.
4. Apply the **unslop** skill to the reply.

throughput checkpoint: n/a, read-only investigation

## Swarm

1. Frame: three read-only exploration slices. Done means source-linked ranked recommendations, checked defects, baseline tests, and explicit validation gaps.
2. Fan out: lifecycle/SimSlim, native/Jev, CLI/MCP/developer UX and testing. Explorers use how defaults because ~/.codex/pstack-models.md is absent.
3. Aggregate: independently check material findings and use a fresh synthesis reviewer.
4. Report: recommendations, tradeoffs, tests, limitations.

## Validation

- Run make test.
- Check CLI and existing service with read-only calls only.
- Do not boot, acquire, input, configure, install, delete, or shut down any simulator in this investigation.

## Completed

- Three read-only explorers completed lifecycle/SimSlim, native/Jev, and developer UX/transport/testing slices.
- A fresh synthesis reviewer checked the combined conclusions against source.
- make test passed; Go race result was cached, and four Python tests ran. The nested SimSlim race suite passed separately.
- Existing CLI fleet and MCP initialize/tools/list/ping checks passed. Twelve rejected MCP tool calls reproduced twelve retained Unix descriptors. The saved reproduction script reproduced the same result.
- Product files and simulator state were not changed. No Xcode or native-action test was run in this investigation.
- Recommendation report: docs/fresh-eyes-review-2026-10-28.md.
