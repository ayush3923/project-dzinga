# project-dzinga

My Agentic Experience.

A production-grade web app on AWS. It is Kubernetes-native, designed to be PCI DSS
compliant, and observable from the start.

- **Engineering rules:** [CLAUDE.md](CLAUDE.md)
- **Architecture decisions:** [docs/adr/](docs/adr/)
- **Monthly cost tracker:** [docs/cost.md](docs/cost.md)

## Stack

| Area | Choice |
|---|---|
| Frontend | TypeScript and Next.js |
| Backend services | Go |
| Contracts between services | OpenAPI 3.1 |
| Infrastructure | AWS CDK (TypeScript) |
| Kubernetes | k3s on EC2 Graviton |
| Region | ap-south-1 (Mumbai) |
| Observability | OpenTelemetry |

## Running locally

Everything runs on your Mac and listens on `127.0.0.1` only. Use three terminal tabs:

```bash
make core-api                               # 1. API + SQLite database in data/
make web                                    # 2. UI at http://127.0.0.1:3000
make preview-yahoo EMAIL=you@yahoo.com      # 3a. dry run: list what it finds, store nothing
make ingest-yahoo EMAIL=you@yahoo.com       # 3b. import into the app
```

Yahoo needs an **app password**: Yahoo Account Security → *Generate app password*.
The importer asks for it with hidden input. It is never stored, logged or put in a
command-line flag. Mail is opened read-only, and only job-related emails are kept,
as a ~400-character snippet each.

To try the app with fictional data, run `make core-api DB=$PWD/data/demo.db`, then `make demo`.

`make test` runs all unit, integration and security tests. `make vuln` scans
dependencies for known vulnerabilities.

## Repository layout

```
apps/web/          Next.js frontend
services/<name>/   Go microservices, one folder each
packages/          Shared TypeScript code: design system, generated API clients
api/               OpenAPI specs, the source of truth for service contracts
infra/             AWS CDK app
deploy/            Helm charts and Kubernetes manifests
docs/adr/          Architecture decision records
```
