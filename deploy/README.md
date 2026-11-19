# Deploying Dzinga to k3s

Images and manifests for the six services (ADR 0006; the quiz service is ADR 0008, the organizations service ADR 0010). **Not yet production ready:** read
the release blockers first.

```
deploy/k8s/base/                     namespace, network policies, one file per service
deploy/k8s/overlays/prod/            ECR image names and tags
deploy/k8s/components/public-ingress/  opt-in public access (blocked, see below)
```

## Release blockers

| # | Blocker | Why it matters | Rule |
|---|---|---|---|
| 1 | Ingest reads mailbox credentials only from the macOS Keychain | Mail sync can't work on Linux. Needs a Secrets Manager or Parameter Store backend behind `secrets.Store` | 1 |
| 2 | The web app has no sign-in, and the `/api/*` routes of core-api, the quiz service and the organizations service have no authentication | Anyone who reaches them sees all job and mail data. NetworkPolicy is the only control today, so don't enable public ingress | 1 |
| 3 | Services talk plain HTTP inside the cluster | Rule 1 requires TLS between services. Needs an owner decision, because a service mesh also uses memory on a small node | 1, 2 |
| 4 | All services share one internal token | The agent fetches arbitrary public URLs, making it the most exposed service. If compromised, it can call every internal route on core-api | 1 |
| 5 | CSP allows `unsafe-inline` scripts | Weakens XSS protection. Needs per-request nonces, as the TODO in `next.config.ts` says | 1 |
| 6 | No OpenTelemetry, audit log or CI (scans, tests) | Required before deploying to production | 1, 7, 8 |
| 7 | `infra/` (CDK) doesn't exist | The settings under "Host requirements" have nothing enforcing them | 2, 3 |

Lower priority: ingest doesn't handle SIGTERM, so a deploy can cut a sync short. The next
sync picks it up again, because emails are de-duplicated by message ID.

## Host requirements (for the CDK stacks)

These controls live outside the manifests, and the design relies on them:

- **IMDSv2 required, with hop limit 1**, so pods can't read the node's IAM credentials.
- **Encrypted EBS root volume (KMS):** the database lives on it. Rule 1.
- **Daily Data Lifecycle Manager snapshots.** SQLite's WAL recovers from a crash-consistent copy.
- **k3s flags:** `--secrets-encryption`, and no `--disable-network-policy`, since that would
  silently turn off every policy here.
- **ECR:** immutable tags, scan on push, and a lifecycle policy. k3s needs the ECR credential
  provider or a refreshed pull secret.
- **Origin:** Traefik accepts traffic from CloudFront only (ADR 0001).

## Deploy

```bash
make k8s-validate
```

```bash
make images REGISTRY=<account>.dkr.ecr.ap-south-1.amazonaws.com
```

In `overlays/prod/kustomization.yaml`, replace `ACCOUNT_ID` and `GIT_SHA`.

Create the secrets from Parameter Store. They are never committed, and piping them keeps
them out of process arguments and shell history. Create the namespace first:

```bash
kubectl create namespace dzinga
```

```bash
aws ssm get-parameter --name /dzinga/internal-token --with-decryption --query Parameter.Value --output text | kubectl -n dzinga create secret generic dzinga-internal --from-file=internal-token=/dev/stdin
```

```bash
aws ssm get-parameter --name /dzinga/anthropic-api-key --with-decryption --query Parameter.Value --output text | kubectl -n dzinga create secret generic dzinga-agent --from-file=anthropic-api-key=/dev/stdin
```

`dzinga-ingest` (`ms-client-id`) and `dzinga-web` (`display-name`) are optional. The internal
token must be at least 32 characters. Then apply:

```bash
kubectl apply -k deploy/k8s/overlays/prod
```

Reach the app without exposing it, over SSM to the node:

```bash
kubectl -n dzinga port-forward svc/web 3000:3000
```

To run without the Claude API, scale the agent to zero:

```bash
kubectl -n dzinga scale deploy/agent --replicas=0
```

**Data safety:** `local-path` deletes the data when the claim is deleted. Don't run
`kubectl delete -k` or delete the namespace without a fresh snapshot. core-api also rewrites
derived data at startup, so take a snapshot before the first rollout of a new version.

## Scanner findings accepted on purpose

| Finding | Reason |
|---|---|
| Image should use a digest; pull policy should be Always | Tags are git commits, and ECR tag immutability (above) stops them changing. Base images are pinned by digest |
| Secrets as env vars rather than files | The services read env vars today. Pods have no shell or API token, which limits exposure. Revisit with blocker 1 |
| No CPU limits | They throttle a small burstable node. Requests are set, and the namespace quota caps the total |
| No Dockerfile `HEALTHCHECK` | Kubernetes probes do this, and the Go images have no shell |
| One replica; no rolling update for core-api | One node and SQLite (ADR 0006) |
