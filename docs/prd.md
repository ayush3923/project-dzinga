# Dzinga: Product Requirements (v0.1)

- **Status:** Draft, first iteration
- **Owner:** Ayush Kumar
- **Date:** 2026-10-14

## 1. Overview

Dzinga is a personal **Work + Personal OS** for job seekers. It pulls in everything about a
job search: intent, discovered roles, applications, interviews, learning and news. It
keeps all of this in one indexed store and uses an agent to tell the user what to do next.

It starts as a single-user tool for the owner and is built to become a multi-user
product. That means per-user data isolation from day one and engineering rules in
[CLAUDE.md](../CLAUDE.md).

## 2. Users and problems

| User | Situation | Problem today |
|---|---|---|
| Active seeker | Applying to many roles | Applications are scattered across 5 mailboxes and many job sites. Follow-ups get missed. |
| Passive explorer | "Just exploring" | No low-effort way to watch for the right opportunity |
| Re-entering the workforce | Career break | Unsure which roles fit, rusty on interview skills |
| Growth seeker | Wants a step up | Hard to compare companies, culture and interview difficulty |

## 3. The nine stages

Each stage is one module. Everything uses one shared, indexed data store.

| # | Stage | What it does | Main inputs |
|---|---|---|---|
| 1 | **Candidate intent** | The user defines target role, designation, location, salary range and company type (startup, scale-up, enterprise, MNC, government), and their situation: *just exploring*, *immediate joiner*, *re-entering the workforce*, *growth*. | User input |
| 2 | **Intent scoring** | Scores every opportunity 0–100 against the intent, and shows why: role match, location, salary, company type, seniority. | Intent and job data |
| 3 | **Discovery** | Finds relevant openings on Indeed, LinkedIn, Naukri, US job sites and local boards, based on intent. | Official APIs, feeds, job-alert emails |
| 4 | **Stack ranking** | Ranks opportunities by intent score, company profile, reviews and reported interview experiences (Glassdoor, Reddit, etc.). | Company data and research |
| 5 | **Follow-ups and reminders** | Tracks *applied* and *to apply* jobs: reminders, nudges for applications gone quiet, drafted follow-ups. | Mailboxes, calendar |
| 6 | **Interview preparation** | Prep packs for each round (screening, hiring manager, technical, and so on), built from the role and the interviewer's public profile, found via email and calendar. | Email, calendar, public research |
| 7 | **Daily quiz** | A short daily pop-up quiz on the user's target skills to keep sharp. | Skills from intent and job descriptions |
| 8 | **Hiring news** | News on hiring at tracked and target companies: layoffs, funding, new offices. | News feeds |
| 9 | **Unique opportunities** | Flags US tech companies opening offices or teams in the user's preferred locations. | News, filings, job feeds |

## 4. Cross-cutting requirements

- **Authentication:** users sign in. In production this is Amazon Cognito with MFA. Every
  record is scoped to one user.
- **Ingestion:** mail sources are Yahoo and iCloud (IMAP), Outlook and live.in (Microsoft
  Graph), and Gmail (Gmail API). All access is **read-only**. Credentials are encrypted
  and never logged.
- **Data minimisation:** store only job-related messages, keep a short snippet rather than
  the full body, and let the user delete any account and its data.
- **Indexed storage:** normalised tables with indexes on user, stage and date, plus
  full-text search. In production: PostgreSQL with pgvector for semantic search.
- **UI:** modern, simple, built on progressive disclosure. The *Today* view shows only
  what matters, and detail opens on demand.
- **Observability:** usage, performance and adoption tracked for each stage (Rule 7).
- **Compliance:** follow the rules in CLAUDE.md. Never scrape a site whose terms forbid
  it; use official APIs, RSS feeds, or emails the user already receives.

## 5. Prototype scope (iteration 1)

**Goal:** see every job application from the owner's **Yahoo** mailbox in one place,
with its current stage. The prototype runs locally.

| In scope | Out of scope (later iterations) |
|---|---|
| Stage 5, partly: applications automatically built from Yahoo mail (applied, assessment, interview, offer, rejected), with a timeline | Other mailboxes, calendar |
| Stage 1: the intent form | Stages 2–4 and 6–9 |
| *Today* view, pipeline board, application detail, search | Sign-in (single local user) |
| Rules-based email classifier with unit tests | Classification and drafting by an AI model (Bedrock) |
| SQLite with FTS5 full-text search | PostgreSQL, AWS deployment |

**Architecture of the prototype** (it mirrors the target microservices):

```
Yahoo IMAP (read-only) ──► ingest (Go) ──HTTP──► core-api (Go) ──► SQLite + FTS5
                                                        ▲
                                         web (Next.js) ─┘
```

## 6. Success metrics

- **Coverage:** at least 90% of real applications in the mailbox detected. Measured by the
  owner reviewing the results.
- **Precision:** at most 5% of detected items are not real applications, such as job
  alerts or newsletters.
- **Time to insight:** the *Today* view answers "what needs my attention?" in under
  10 seconds.
- **Adoption, after launch:** weekly active use; share of follow-ups actioned.

## 7. Open questions

1. Discovery sources: which job sites offer usable APIs or feeds? LinkedIn and Glassdoor
   forbid scraping.
2. AI model budget for classification, scoring, prep packs and quizzes.
3. Monetisation, and whether payment is ever needed (Rule 1 PCI scope).
