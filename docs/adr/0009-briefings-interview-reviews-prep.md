# ADR 0009: Organization briefings, interview reviews and prep questions

- **Status:** Accepted
- **Date:** 2026-11-19
- **Builds on:** ADR 0007 (Claude's work queue), ADR 0008 (the quiz service)
- **Amended by:** ADR 0010 (briefings moved to the organizations service)

## Context

The daily quiz already makes Claude read recent news on four companies the user has
applied to, but what it learns stays in the quiz. The user also wants:

- that news, plus each company's CEO, product head and latest revenue, on the
  organization, with a sign that there is something new to check;
- a place to write how an interview went and get a read on their chances;
- three bonus interview-prep questions a day on the Today page.

All three need judgment or web research, so under ADR 0007 Claude does them in the
Claude app, at no API cost.

## Decision

- **Briefings live in core-api**, on the organization (`org_briefings`,
  `org_briefing_news`), because they describe the company, not the quiz. Claude saves
  one per chosen company while building the quiz
  (`PUT /internal/v1/organizations/{id}/briefing`). Facts are replaced on each save;
  news accumulates by link and keeps the 10 newest. Revenue is stored as published
  text with its period and source, since companies report in different currencies,
  units and financial years. The quiz service still never calls core-api.
- **"Something new" flag:** an organization is flagged while its briefing was updated
  after the user last opened it. Opening the drawer clears it
  (`POST /api/v1/organizations/{id}/briefing/seen`).
- **Interview notes and reviews live in core-api**, on the application. Saving a note
  (only in the `interview` stage) queues a new kind, `interview_review`; Claude saves
  the review (`chance` low/medium/high, summary, signals, concerns, next steps), which
  completes it. Reviews are kept; the newest is shown. Merging two applications moves
  both. The queue's CHECK constraint is rebuilt (migration v13).
- **Prep questions live in the quiz service** (`prep_questions`, migration v3), since
  they are daily practice like the quiz. Exactly three a day; `pending.prep` lists
  today when they are missing (a past day's are no use). They aim at live interviews first and are not graded.

## Consequences

- No new service, process or AWS cost; three tables in core-api, one in the quiz
  service, and more work in each scheduled Claude run.
- Interview notes are the user's own account of a conversation with a third party. They
  stay in the local database like the rest of the data and go only to Claude in the app.
- Briefing facts can go stale or be wrong; the drawer shows the source and date of each
  and asks the user to check before quoting.
