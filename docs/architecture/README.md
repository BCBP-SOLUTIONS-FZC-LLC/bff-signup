# Architecture diagrams

Standalone Mermaid source files. Three of them are embedded verbatim elsewhere, and no test keeps the copies in sync, so change every copy together:

- `layer-model.mmd` and `provisioning-flow.mmd` = the diagrams in `ARCHITECTURE.md` (§ Layer model, § SB-2 happy path);
- `handoff-state-machine.mmd` = `ARCHITECTURE.md` § Handoff state machine = LLD §9.1.

`package-dependencies.mmd` and `valkey-keys.mmd` are only here.

To render locally, open any `.mmd` file in a Mermaid-aware IDE (VS Code + Mermaid Preview) or paste into [mermaid.live](https://mermaid.live).

| File | Diagram | Related section |
|---|---|---|
| [`layer-model.mmd`](mermaid/layer-model.mmd) | Clean Architecture layer graph — cmd/server, inbound/http, outbound adapters (incl. billing), core, pkg/signupctx, tests | ARCHITECTURE.md § Layer model |
| [`package-dependencies.mmd`](mermaid/package-dependencies.mmd) | Go import graph (mirrors `.go-arch-lint.yml`) | `.go-arch-lint.yml` |
| [`provisioning-flow.mmd`](mermaid/provisioning-flow.mmd) | SB-2 happy path: CAPTCHA → rate limit → validate → hash → eligibility → PUB-1 → RP-1 → I-1 | ARCHITECTURE.md § Request flows, LLD §8.1 |
| [`handoff-state-machine.mmd`](mermaid/handoff-state-machine.mmd) | 10-state handoff machine, trial and paid paths (§19) | ARCHITECTURE.md § Handoff state machine, LLD §9.1 |
| [`valkey-keys.mmd`](mermaid/valkey-keys.mmd) | Valkey key namespace, TTLs, access patterns | ARCHITECTURE.md § Valkey key catalogue, LLD §4.1 |
