# ADR 0008: The daily quiz is its own service

- **Status:** Accepted
- **Date:** 2026-11-17
- **Amends:** ADR 0007 (the quiz part of Claude's work queue)

## Context

ADR 0007 first built the daily quiz inside core-api. The quiz has its own data
(quizzes, answers, recordings), its own users of that data (the quiz page and Claude),
and nothing in common with applications except the list of companies Claude picks news
from. Rule 5 asks for microservices with clear ownership.

## Decision

- **`services/quiz`** (Go, port 8084) owns quizzes, questions, answers, feedback and the
  recordings, in its own SQLite database (`data/quiz.db`) and audio folder. core-api's
  migration v11 drops its quiz tables.
- **Refresh:** after core-api's maintenance step, the sync service calls the quiz service's
  `POST /internal/v1/requests` to ask for today's quiz. A failure is logged and doesn't fail
  the refresh.
- **Claude:** reads `GET /internal/v1/pending` (quizzes to build, answers awaiting feedback)
  and saves through the quiz service's internal API. It gets the company list from core-api
  itself, so the quiz service never calls core-api.
- **Web:** the quiz page and recording playback call the quiz service (`QUIZ_API_URL`).
- **Cluster:** its own Deployment (one replica, `Recreate`), 2 GiB volume on the server node,
  probes, limits, non-root, read-only root filesystem. NetworkPolicy lets only web and
  ingest reach it, and it has no egress.

## Consequences

- One more process locally (`make quiz`) and one more image and volume in the cluster
  (about 32 MiB of memory; no new AWS line item).
- A quiz or recording problem can't affect application data, and the quiz can be changed
  or switched off without touching core-api.
- The quiz service's `/api` routes have no authentication yet, like core-api's: release
  blocker 2 in deploy/README.md covers both.
