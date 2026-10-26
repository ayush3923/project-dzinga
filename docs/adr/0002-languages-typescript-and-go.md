# ADR 0002: TypeScript for the frontend and infrastructure, Go for backend services

- **Status:** Accepted
- **Date:** 2026-10-14

## Context

Rule 6 requires a specific, compatible set of languages. We want strong typing
throughout, a small runtime footprint (the budget is tight and pods must stay small), and
a mature ecosystem for security and observability.

## Decision

| Layer | Language | Framework and tooling |
|---|---|---|
| Web frontend | TypeScript (strict) | Next.js (App Router) and React; pnpm; Vitest; Playwright for end-to-end tests |
| Backend microservices | Go | Standard library `net/http` with a thin router; `sqlc` for type-safe SQL; `go test` and Testcontainers |
| Infrastructure as code | TypeScript | AWS CDK v2 (see ADR 0004) |
| Service contracts | OpenAPI 3.1 | Generate the TypeScript clients and Go server stubs from the specs, so the two languages never drift apart |
| Observability | OpenTelemetry | OTel SDKs for Go and JavaScript |

Rules that follow from this:

- **No other languages** without a new ADR. Shell scripts are allowed only for glue
  code, and must pass `shellcheck`.
- **Pin versions.** Use the current Node.js LTS (pinned in `.nvmrc` and `engines`) and the
  current stable Go (pinned in `go.mod` with a `toolchain` line). All TypeScript
  dependencies are locked in one pnpm workspace.
- **Container images:** Go services compile to static binaries on `distroless/static`
  (arm64), which gives a small attack surface and small images. Next.js runs in
  `standalone` output mode on a minimal Node image.

## Consequences

- **Benefits:**
  - Go services start fast and use little memory, so more fits on a `t4g` node.
  - The frontend and infrastructure share a language and tooling.
- **Costs:** two toolchains in CI. OpenAPI code generation is how the two sides stay
  compatible.
