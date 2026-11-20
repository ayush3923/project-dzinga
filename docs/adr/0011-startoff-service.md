# ADR 0011: Startoff runs the daily jobs and owns company research

- **Status:** Accepted
- **Date:** 2026-11-20
- **Amends:** ADR 0010 (research leaves the organizations service), ADR 0008 (who asks for the quiz)

## Context

The daily work was spread out and only happened when someone pressed Refresh: the sync
service, after a refresh, asked the quiz service for the day's quiz and the
organizations service for diagnoses. Company research lived in the organizations service
and kept only the latest of most things: a new profile replaced the old one, quiz facts
were overwritten and only the 10 newest news items were kept. The owner wants the
analysis, news and diagnoses kept, and the day's jobs to run on their own.

## Decision

- **`services/startoff`** (Go, port 8086, `data/startoff.db`) owns:
  - **Company research, with history.** Every profile version (with its news, products,
    leaders and ratings), a row of quiz facts each time they change, every quiz news
    item (each link once per company) and every diagnosis. The newest of each is what
    the organization drawer shows; `GET /api/v1/organizations/{id}/history` returns all.
  - **Claude's research queue** (`company_profile`, `diagnosis`), as before (ADR 0007).
  - **The daily run.** At 07:00 Asia/Kolkata (`DZ_DAILY_AT`, `DZ_TIMEZONE`), or when the
    Refresh button or Claude asks: refresh every mailbox (sync service), request the
    day's quiz (quiz service) and top the queue up to 5 diagnoses picked at random from
    the organizations service's list. Each run and step is recorded. The timer checks
    every minute, so a run missed while the Mac slept starts when it wakes; a run that
    already started after 07:00 that day counts.
- **Organizations keep their ids.** Research is keyed by the organizations service's id;
  startoff checks the id (and learns the name) with that service on every save or
  request. There is no foreign key across services.
- **Dependencies point one way:** web and agent → startoff → sync, quiz, organizations.
  The sync service no longer calls the quiz or organizations services; a refresh of
  chosen mailboxes from the Profile page is just a refresh.
- **The web app joins the two:** the organizations list and drawer read organizations
  from the organizations service and research from startoff. If startoff is down, the
  pages still show organizations, without research.
- **Move:** `make startoff-import FROM=<orgs.db at v2, or a backup>` copies the research
  once, keeping ids; each profile and briefing becomes the first version of its history.
  The organizations service's migration v3 then drops those tables.
- **Cluster:** its own Deployment (one replica, `Recreate`), 1 GiB volume on the server
  node, probes, limits, non-root, read-only root filesystem. NetworkPolicy lets web and
  agent reach it; its egress is the sync, quiz and organizations services only.
- Claude still does the research and writing in the Claude app (ADR 0007); startoff
  only schedules and keeps it. No Claude API calls, no new AWS line item.

## Consequences

- One more process locally (`make startoff`) and one more image and volume in the
  cluster (about 32 MiB of memory).
- History grows slowly: a few kilobytes per company per day at most.
- On a laptop, 07:00 means "the first minute after 07:00 that the Mac is awake and the
  service is running".
- Order of the move: import before the organizations service's v3 runs, or from a backup
  taken before it.
