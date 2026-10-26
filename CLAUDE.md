# Project Dzinga: Engineering Rules

A production-grade web app intended for real paying customers. These rules apply to
every change. When a rule conflicts with speed or convenience, the rule wins. When two
rules conflict with each other, stop and raise it with the owner instead of choosing silently.

Repo: https://github.com/ayush3923/project-dzinga (private, default branch `main`)

---

## Rule 1: Security first, PCI DSS compliant

- Treat PCI DSS v4.0 as a hard requirement.
- **Keep card data out of our systems.** Use a PCI Level 1 payment provider's hosted
  fields or checkout, such as Stripe Checkout or Elements. Our services must never
  receive, store, log or transmit a PAN, CVV or track data. This keeps our scope as
  small as possible (SAQ A).
- **Secrets** go in AWS Secrets Manager or SSM Parameter Store (SecureString). Never put
  them in code, images, env files committed to git, or logs.
- **Encryption:** TLS 1.2+ everywhere, including between services. Data is encrypted at
  rest with KMS.
- **Access:** least-privilege IAM. Pods get only their own permissions through IRSA or Pod
  Identity. No long-lived access keys. Every account has MFA.
- **Validation:** validate every input at service boundaries. Use parameterized queries
  only. Use output encoding and a strict Content Security Policy.
- **Dependencies:** lock files are committed. CI runs dependency scanning, container image
  scanning (ECR or Trivy), secret scanning, and static analysis (SAST).
- **Audit logs** record who did what and when, cannot be altered, and are kept for at
  least 12 months (PCI DSS 10.5).
- **When unsure whether something is vulnerable, write a test that tries to exploit it.**
  For example: injection, broken authentication or authorization, IDOR, SSRF, XSS, CSRF,
  missing rate limits. Security tests live with the regular tests and run in CI.

## Rule 2: AWS native and frugal (≤ USD 100/month to start)

- Prefer managed AWS services. Choose the cheapest option that meets the other rules.
- Every new infrastructure component needs a monthly cost estimate before it is added.
  Track the running total in `docs/cost.md`.
- Use AWS Budgets alerts at 50%, 80% and 100% of USD 100.
- Defaults that save money:
  - Graviton (arm64) instances.
  - Spot instances where interruption is tolerable.
  - Avoid NAT Gateways; use VPC endpoints or public subnets with tight security groups.
  - A single region to start.
  - Short log retention, except audit logs.
  - Scale to zero wherever possible.
- All infrastructure is code (Terraform or CDK). Nothing is created by clicking in the console.

## Rule 3: AWS Well-Architected Framework

Design reviews cover all six pillars:
- Operational Excellence
- Security
- Reliability
- Performance Efficiency
- Cost Optimization
- Sustainability

Record significant decisions as ADRs in `docs/adr/` (one Markdown file per decision:
context, options, decision, consequences).

## Rule 4: Modern, intuitive, unique design with progressive disclosure

- Show only what the user needs for the current step. Put advanced options behind clear
  affordances such as expanders, "More options" links, multi-step flows, and contextual
  detail panels.
- Use one design system: tokens for color, spacing and typography, with light and dark
  themes.
- Meet WCAG 2.2 AA accessibility. Design mobile-first.
- Avoid generic template looks. The product should have its own visual identity.

## Rule 5: Kubernetes-based microservices

- Each service owns its own data and is deployed independently. Services talk over
  well-defined APIs (OpenAPI or gRPC) or events.
- Each service ships as a container with Helm or Kustomize manifests, health and
  readiness probes, resource requests and limits, and runs as non-root with a read-only
  filesystem.
- Do not split services more finely than the team and budget can operate. Start with a
  few coarse services.

## Rule 6: Specific, compatible languages

The stack is fixed; see "Decisions" below. Do not introduce a new language, framework or
major library without an ADR. Pin versions.

## Rule 7: Observability at the core

- Every service is instrumented with OpenTelemetry for traces, metrics and logs from the
  first commit.
- Logs are structured JSON, include a correlation (trace) ID, and **never contain
  PII, secrets or card data**.
- Measure three things everywhere:
  - **Usage:** requests and feature events.
  - **Performance:** latency, errors and saturation (RED and USE metrics), plus Core Web
    Vitals.
  - **Adoption:** funnels, active users, feature usage.
- Each user-facing feature defines its analytics events before it is built.
- Dashboards and alerts are kept as code.

## Rule 8: Testing

- Unit tests and integration tests are required for every change. Integration tests run
  against real dependencies in containers (Testcontainers or docker compose).
- End-to-end tests cover the critical user flows.
- CI blocks merging when tests, lint, type checks or security scans fail.
- Report coverage. New code must be covered.

---

## Decisions (filled in as they are made)

| Topic | Decision | ADR |
|---|---|---|
| Kubernetes flavour | k3s on EC2 Graviton: 1 on-demand server node, spot agent nodes | [0001](docs/adr/0001-kubernetes-k3s-on-ec2.md) |
| Languages and frameworks | TypeScript with Next.js (frontend); Go (services); OpenAPI 3.1 contracts | [0002](docs/adr/0002-languages-typescript-and-go.md) |
| AWS region | ap-south-1 (Mumbai); edge resources in us-east-1 | [0003](docs/adr/0003-region-ap-south-1.md) |
| Infrastructure as code tool | AWS CDK v2 (TypeScript) with cdk-nag | [0004](docs/adr/0004-iac-aws-cdk.md) |
| AI model routing | Per task: retrieval Sonnet 4.6, summary Sonnet 5, correlation and scoring Opus 5.5; escalate once on failure | [0005](docs/adr/0005-llm-model-router.md) |
| Database | _open_: RDS PostgreSQL or Postgres inside the cluster | |
| Observability backend | _open_: CloudWatch, or self-hosted Grafana with Prometheus and Loki | |

Repo layout and cost tracker: see [README.md](README.md) and [docs/cost.md](docs/cost.md).
