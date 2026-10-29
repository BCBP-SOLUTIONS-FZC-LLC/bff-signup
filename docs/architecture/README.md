# Architecture diagrams

Standalone Mermaid source files. `ARCHITECTURE.md` embeds every one of these `.mmd` files verbatim as a fenced code block, each under a `> Source:` link back to the file here — so a diagram only needs to be correct in one place. Keep both in sync by hand when either changes.

To render locally, open any `.mmd` file in a Mermaid-aware IDE (VS Code + Mermaid Preview) or paste into [mermaid.live](https://mermaid.live).

| File | Diagram | Related section |
|---|---|---|
| [`layer-model.mmd`](mermaid/layer-model.mmd) | Clean Architecture layer graph — cmd/server, inbound/http, outbound adapters, core, pkg/signupctx | ARCHITECTURE.md § Layer model |
| [`package-dependencies.mmd`](mermaid/package-dependencies.mmd) | Go import graph (mirrors `.go-arch-lint.yml`) | `.go-arch-lint.yml` |
| [`provisioning-flow.mmd`](mermaid/provisioning-flow.mmd) | Full SB-2 happy path: CAPTCHA → rate limit → eligibility → RP-1 → I-1 | ARCHITECTURE.md § Request flows, LLD §8.1 |
| [`handoff-state-machine.mmd`](mermaid/handoff-state-machine.mmd) | 7-state handoff machine (allocated → completed / failed / slug_conflict) | ARCHITECTURE.md § Handoff state machine, LLD §9.1 |
| [`valkey-keys.mmd`](mermaid/valkey-keys.mmd) | Valkey key namespace, TTLs, access patterns | ARCHITECTURE.md § Valkey key catalogue, LLD §4.1 |
