# agent-phone

Local, resource-conscious iOS simulator control for agents. Baseline hardware is an 8 GB Apple Silicon Mac. The demonstration app is the official open-source Wikipedia iOS app, never Indubitably.

- Keep phone control independent of model providers and API keys.
- Use explicit simulator IDs. Mutations require a session lease; never manipulate an unmanaged simulator.
- Keep native helpers persistent per active phone. Validate the selected target immediately before input; unrelated changing labels must not invalidate it.
- Accessibility and screenshots are complementary observations, with separate capture timestamps. Do not claim atomic capture or success from an input acknowledgment alone.
- Preserve upstream licenses and record source revisions in THIRD_PARTY.md. Do not import the firmware patching or VM signing requirements into the Simulator backend.
- Serialize Xcode builds on this hardware. Keep build outputs isolated by project and configuration.
- Do not discard user changes or install global tools as part of setup. Dependencies and demo checkouts live in ignored local directories.
- Run `make test` and the relevant live checks. Update documentation with actual results and limitations.
