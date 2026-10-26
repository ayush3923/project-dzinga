# ADR 0005: Route agent tasks to different Claude models

- **Status:** Accepted
- **Date:** 2026-10-18

## Context

The agent does very different work: reading the web, extracting structure, reasoning
about fit, and writing. One model for everything either overpays for simple steps or
underpowers the hard ones. Rule 2 asks us to be frugal; Rule 7 asks us to measure.

## Decision

A router in `services/agent/internal/router` chooses the model per task. The default
routes were chosen by the product owner:

| Task | What it does | Model | Effort | Retried on failure with |
|---|---|---|---|---|
| Retrieval | Web search and fetch to find the posting; free-text notes | `claude-sonnet-4-6` | medium | `claude-opus-5-5` |
| Summary | Notes → structured posting (JSON) | `claude-sonnet-5` | medium | `claude-opus-5-5` |
| Correlation | Each requirement → evidence in the profile (strong, partial, missing) | `claude-opus-5-5` | high | — |
| Scoring | Five-factor fit score from posting, evidence and intent | `claude-opus-5-5` | high | — |
| Drafting | Cold email on request (no model named; defaults to the strongest) | `claude-opus-5-5` | high | — |
| Company research | Web research for a company profile (added 2026-10-18) | `claude-sonnet-5` | medium | `claude-opus-5-5` |
| Company profile | Notes → structured profile plus interview angle | `claude-sonnet-5` | medium | `claude-opus-5-5` |
| Diagnosis | Why a company is silent or only rejects | `claude-sonnet-5` | high | `claude-opus-5-5` |

Rules the router enforces:

- **Capabilities per model.** Each model is sent only the settings it accepts:
  - Sonnet 4.6 has no structured outputs, so it can only run retrieval. It needs
    `thinking: adaptive` sent explicitly.
  - Opus 5.5 rejects any thinking setting; it gets server-side refusal fallbacks.
  - Overrides that break a capability are refused at startup.
- **Escalation.** A refusal, invalid JSON, a cut-off answer or a server error (5xx) is
  retried once on the route's stronger model. A bad API key or a rate limit is not
  retried.
- **Overrides.** `DZ_ROUTE_<TASK>=<model>` changes a route without code changes, for
  example `DZ_ROUTE_SUMMARY=claude-opus-5-5`.
- **Measurement.** Every call is stored in `llm_usage`, with the task (marked
  `:escalated` for retries), the model, tokens and web searches. Each fit score records
  its pipeline (`retrieval=…;summary=…;…`).

## Consequences

- **Cost:** retrieval and summary run on cheaper Sonnet models, and Opus is used only
  where judgement matters.
- **Complexity:** a fit score is now four calls instead of two, so it adds latency. The
  correlation output is stored (`fit_evidence`, `fit_points`) and shown to the user.
- **Upkeep:** model capabilities change. The capability table in `router.go` must be
  updated when models are added or retired, and its tests catch invalid routes.
