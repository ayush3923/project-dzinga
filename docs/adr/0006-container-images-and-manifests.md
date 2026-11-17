# ADR 0006: Container images and Kubernetes manifests

- **Status:** Accepted
- **Date:** 2026-11-04

## Context

Rule 5 requires each service to ship as a container with manifests, probes, resource
limits, a non-root user and a read-only filesystem. The prototype runs on one Mac, keeps
its data in SQLite, and has no sign-in. ADR 0001 gives us one on-demand k3s server node and
optional spot agent nodes.

## Options

1. **Helm charts:** templating we don't need for four services and one environment.
2. **Kustomize base and overlay:** plain YAML, built into `kubectl`.

## Decision

- **Images:** one per service, built from that service's own folder, so `.env.local` and
  `data/` at the repo root can't enter a build context. Base images are pinned by digest.
  Go services use `distroless/static`. The web app uses Next.js standalone output on Node
  Alpine with the package managers removed.
- **Manifests:** Kustomize in `deploy/k8s`. Namespace at the `restricted` Pod Security level,
  default-deny NetworkPolicies, one ServiceAccount per service with no API token.
- **Storage:** core-api stays a single replica with the `Recreate` strategy on a `local-path`
  volume, pinned to the on-demand server node. SQLite allows one writer, and a spot node
  would take the data with it. Backups are EBS snapshots through Data Lifecycle Manager.
- **Public access:** an opt-in Kustomize component, not part of the overlay, until the app
  has sign-in.

## Consequences

- **Benefits:** small images, no shell in the Go images, and no new running components.
- **Costs:** core-api has downtime on each deploy and on node loss. This is revisited when
  the open database decision is made.
- **Gaps:** this does not make the app production ready. `deploy/README.md` lists the release
  blockers, including where the current design falls short of Rule 1.
