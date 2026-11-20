# ADR 0010: Organizations are their own service

- **Status:** Accepted
- **Date:** 2026-11-19
- **Amends:** ADR 0009 (where briefings live)

## Context

core-api held applications and, beside them, everything about the companies behind
them: the preferred list, company profiles, diagnoses, and since ADR 0009 the briefings
from the daily quiz. Those have their own writers (the user, Claude, the agent), their
own page, and grow independently of mail. Rule 5 asks for microservices with clear
ownership, as ADR 0008 did for the quiz.

## Decision

- **`services/orgs`** (Go, port 8085) owns organizations, the preferred list, company
  profiles, diagnoses, briefings, Claude's queue (`company_profile`, and `diagnosis`
  since the update below) and the "worth a look" suggestions, in its own SQLite database (`data/orgs.db`).
- **No stored link between services.** An application belongs to the organization whose
  key (lower-case letters and digits of the name) matches its company's. The service
  reads core-api's `GET /api/v1/analysis` on each request, creates organizations for new
  companies and computes the statistics. The web app links an application to its
  organization by company name (`?company=`, resolved with `GET .../lookup`).
- **Dependencies point one way:** orgs calls core-api; core-api never calls orgs. So the
  morning digest (core-api) no longer lists suggested companies, which need the
  preferred list: the Today page reads them from orgs, and the agent adds them when it
  rewrites the digest.
- **Move:** `make orgs-import FROM=<core db>` copies the tables once, keeping ids, into an
  empty `orgs.db`, reading the source read-only (a backup works). core-api's migration
  v14 then drops them, removes `applications.organization_id` and the queued
  `company_profile` requests.
- **Cluster:** its own Deployment (one replica, `Recreate`), 1 GiB volume on the server
  node, probes, limits, non-root, read-only root filesystem. NetworkPolicy lets web,
  ingest and agent reach it; its only egress is core-api.
- **Update (2026-11-20): diagnoses by Claude in the app.** After each refresh the sync
  service calls `POST /internal/v1/diagnosis-requests`, which tops the queue up to 5 `diagnosis` requests for
  organizations picked at random (with applications, none already queued, and no
  diagnosis or one out of date). Claude works them per the runbook, as with profiles
  (ADR 0007), instead of the agent's rules-plus-Sonnet path. A new diagnosis flags the
  organization on the list until it is opened.

## Consequences

- One more process locally (`make orgs`) and one more image and volume in the cluster
  (about 32 MiB of memory; no new AWS line item).
- Every organization view costs one extra local HTTP call to core-api. At one user's
  scale (hundreds of applications) that is milliseconds; if it ever isn't, core-api can
  serve a slimmer per-company endpoint.
- If core-api is down, the Organizations page shows the service as unreachable rather
  than stale numbers.
- The order of the move matters: import before core-api's v14 runs, or import from a
  backup taken before it.
