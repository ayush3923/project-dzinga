# ADR 0007: Claude does judgment work through a queue, on a schedule

- **Status:** Accepted; the quiz part is amended by ADR 0008 (the quiz is its own service)
- **Date:** 2026-11-17

## Context

The owner wants every refresh to give new applications an Opus fit score and to build a
daily quiz (AI news, product concepts, news on companies applied to) with written and
spoken answers that get feedback. The cost rule says only the short summary may use the
Claude API; analysis is done in the Claude app on the owner's subscription. The API account
has no credit. A click in the web app can't start a Claude session.

## Options

1. **Claude API from the agent service** right after each refresh. Fully automatic, about
   $0.05–0.15 per fit score or quiz on Opus, against the cost rule.
2. **Queue, drained when the owner asks.** No cost, but nothing happens on its own.
3. **Queue, drained by a scheduled task in the Claude app.** No API cost; runs while the
   Mac is awake and the Claude app is open.

## Decision

Option 3, with option 2 still available.

- A refresh's last step queues a `fit_score` request for each new open application without
  a score (at most 20; none on the very first refresh) and one `daily_quiz` per IST day.
- Written and spoken answers queue `quiz_feedback`. The web app can also queue a fit score.
- These share `research_requests` with the existing research kinds; saving the result marks
  the request done.
- A scheduled Claude task follows `docs/runbooks/claude-queue.md`. It calls the API only
  through `scripts/dzq.sh`, which keeps the internal token out of prompts, logs and
  process listings.
- Spoken answers are recorded in the browser and stored as files on core-api's disk (mode
  0600, server-generated names, type checked by content, 8 MB limit). They are transcribed
  on the Mac with whisper.cpp, never by a third-party speech service.

## Consequences

- Fit scores and quizzes arrive on the next scheduled run, not instantly.
- Nothing runs while the Mac sleeps or the Claude app is closed.
- In the cloud deployment (ADR 0001) this needs another worker: the API path (option 1)
  with a budget, or a Claude worker with its own credentials. Decide before deploying.
- The microphone is allowed for the app's own origin only (`Permissions-Policy`).
