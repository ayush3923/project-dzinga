# Data model

core-api owns applications and everything about them; the quiz service (ADR 0008) and the
organizations service (ADR 0010) own their own tables, below. Every table carries `user_id`
(directly or through its parent), and every query filters on it.
Local deployments use SQLite (`services/core-api/internal/store/migrate.go`). Production
uses PostgreSQL with the same tables, plus row-level security on `user_id`.

## Tables

| Table | One row per | Key columns | Written by |
|---|---|---|---|
| `applications` | job application (company + role) | company, role, stage, stage_override, applied_at, last_activity_at, rounds_done, next_step | ingest (via core-api), user (stage override, interview progress) |
| `events` | job-related email | message_id (unique per user), account, occurred_at, sender, sender_name, subject, snippet (≤400 chars), kind, job_url | ingest |
| `stage_history` | step in an application's path | stage (applied, shortlisted, assessment, interview, offer, rejected), round, entered_at, event_id, source (email or manual) | core-api, rebuilt from `events` |
| `contacts` | person who emailed the user about an application | name, email, role_hint, source (mailbox or posting) | core-api, from received mail only |
| `job_postings` | researched posting behind an application (1:1) | status, source (email_link, web_search, none), url, title, location, work_mode, seniority, min_years, product_context, hiring_manager_name / _title | agent |
| `job_posting_items` | requirement, qualification or responsibility line | kind, position, text | agent |
| `fit_scores` | fit-score run (history kept; latest is current) | total (0–100), verdict, model, pipeline (model per stage), created_at | agent |
| `fit_factors` | one of five factors per run | factor (experience, location, seniority, role_alignment, interest_intent), score (0–20), rationale | agent |
| `fit_suggestions` | improvement suggestion per run | position, text | agent |
| `fit_evidence` | requirement mapped to the profile (correlation stage) | requirement, evidence, strength (strong, partial, missing) | agent |
| `fit_points` | strength or gap per run | kind (strength, gap), position, text | agent |
| `outreach_drafts` | cold-email draft (never sent) | contact_id, subject, body, product_analysis, model | agent, on request |
| `outreach_points` | supporting bullet per draft | kind (experience, value_add, improvement), position, text | agent |
| `profiles` | candidate profile | full_name, headline, current_title, current_company, total_years, location, summary | user |
| `profile_experiences` | role in the profile | position, title, company, start_month, end_month, location | user |
| `profile_highlights` | achievement under a role | position, text | user |
| `profile_skills` | skill | skill | user |
| `profile_photos` | profile photo (the image is a file on core-api's disk) | updated_at | user (upload, remove) |
| `intents` | job-search intent (PRD stage 1) | roles, designation, locations, salary range, company types, situation | user |
| `interview_notes` | the user's notes on an interview round | round (0 = not given), body, created_at | user (only in the interview stage) |
| `interview_reviews` | Claude's read on the notes (history kept; latest shown) | chance (low, medium, high), summary, signals, concerns, next_steps, model | Claude (scheduled run) |
| `research_requests` | queued work for Claude (ADR 0007) | kind (job_posting, fit_score, interview_review), target_id (an application), status (pending, done) | web app, refresh, a saved interview note; done when the result is saved |
| `llm_usage` | Claude API call | purpose, model, input_tokens, output_tokens, web_searches | agent (cost tracking, Rule 7) |

## Organizations service tables (`data/orgs.db`, ADR 0010)

Applications are grouped into organizations by company key (lower-case letters and digits
of the name), read from core-api's analysis on each request.

| Table | One row per | Key columns | Written by |
|---|---|---|---|
| `organizations` | company (normalised name) | key, name | orgs, the first time an application to it is seen; user (preferred list) |
| `org_settings` / `org_preferences` | preferred organizations | preferred_mode (auto = top 3, manual), ordered list | user |
| `company_profiles` | researched company (1:1 with organization) | overview, interview_angle, model | Claude (scheduled run) or agent |
| `company_news` / `company_products` / `company_leaders` | profile details | news ≤ 5 from the last 3 months with date and link; products; leaders | as above |
| `company_ratings` | external rating | source (glassdoor = employees, gartner_peer_insights = customers), score (NULL = not found), review_count, url, as_of | as above |
| `org_diagnoses` / `org_diagnosis_items` | diagnosis run | summary, causes with confidence, actions | agent (Sonnet 5) |
| `org_briefings` | what the daily quiz found on a company (1:1) | ceo, cpo, revenue_year / _quarter (value as published, period, source url), leaders_url, updated_at, seen_at | Claude (scheduled run); seen_at by the user opening it |
| `org_briefing_news` | dated news item on a company (10 newest kept) | day (quiz day), title, summary, source_title, url (unique), published_on | Claude |
| `research_requests` | queued company profile (ADR 0007) | organization_id, status (pending, done) | web app; done when the profile is saved |

## Quiz service tables (`data/quiz.db`, ADR 0008)

| Table | One row per | Key columns | Written by |
|---|---|---|---|
| `quizzes` | daily quiz (one per IST day) | day, model | Claude (scheduled run) |
| `quiz_questions` | one of 7 questions per quiz | position, kind (mcq ×5, subjective, voice), topic (ai_news, pm_concept, org_news), prompt, options, answer, explanation, source_title, source_url | Claude |
| `quiz_answers` | the user's answer to a question | choice, text, audio_file (name only; file in the service's audio folder), transcript, feedback, score (0–10) | user; Claude adds feedback |
| `quiz_requests` | day whose quiz a refresh asked for | day, requested_at, done_at | sync service (refresh); done when the quiz is saved |
| `news_items` | key news item for a day (shown on the profile) | day, position, topic (ai_news, org_news), company, title, summary, source_title, url, published_on | Claude, with the daily quiz |
| `prep_questions` | one of three bonus prep questions a day (Today page) | day, position, company, role, question, why, approach | Claude, with the daily quiz |

## Relationships

- An application has many `events` and many `stage_history` steps. Steps are derived from
  events, so a rule change re-applies to old mail on the next start.
- An application has at most one `job_postings` row, which has many `job_posting_items`.
- An application has many `fit_scores`, and each score has exactly five `fit_factors`
  (the total equals their sum, enforced by the API).
- An application has many `contacts` and many `outreach_drafts`. A draft may point at a
  contact of the same application only.
- An application has many `interview_notes` and `interview_reviews`; merging two
  applications moves both.
- Across services, an application belongs to the organization whose key matches its
  company's; nothing stores the link, so correcting the company moves it.

## Derived rules

- **Shortlisted** is when the company first moved the user forward: the first assessment or
  interview email.
- **A new interview round** starts with an invitation that is not a reminder, reschedule,
  confirmation or NDA, and arrives at least 2 days after the previous round began.
- **One application per acknowledgement:** a "thank you for applying" email starts a new
  application, unless another acknowledgement for the same company arrived within 2 days.
  Role-less rejections and interviews join the company's latest earlier application.
- **Diagnosis is suggested** for a company that hasn't replied to any application after
  7 days, or whose every reply was a rejection.
- **A new cycle** starts when a fresh application email arrives after a decision at the
  same company. The timeline then follows the latest cycle.
- **Days in stage** = now − the latest step's `entered_at`.
- **Time to decision** = the decision step − the first step.

## Privacy

- Only job-related emails are stored, as a ≤400-character snippet.
- Contacts are only people who emailed the user, or who are named in a public posting.
  Email addresses are never guessed.
- Drafts are never sent by Dzinga.
- The profile photo is re-encoded on upload, which removes EXIF and other metadata
  (including GPS location), and stored only on core-api's disk (mode 0600).
- Spoken quiz answers stay on the quiz service's disk and are transcribed on the same
  machine, never by a third-party speech service.
