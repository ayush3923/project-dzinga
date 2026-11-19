import "server-only";

// Typed client for core-api (contract: api/core-api.yaml). Called only from the
// server, so core-api is never exposed to the browser.

export type Stage = "applied" | "assessment" | "interview" | "offer" | "rejected";
export type EventKind = Stage | "update";

export interface Application {
  id: number;
  company: string;
  role: string;
  stage: Stage;
  stage_is_manual: boolean;
  source_account: string;
  accounts: string[];
  applied_at: string;
  last_activity_at: string;
  event_count: number;
  organization_id: number;
  job_url: string;
  rounds_done: number;
  next_step: string;
}

export interface MailEvent {
  id: number;
  account: string;
  occurred_at: string;
  sender: string;
  subject: string;
  snippet: string;
  kind: EventKind;
}

export interface RecentEvent extends MailEvent {
  application_id: number;
  company: string;
  role: string;
}

export interface Summary {
  total: number;
  by_stage: Record<Stage, number>;
  needs_nudge: Application[];
  recent_events: RecentEvent[];
}

// Verdict band for a fit total; core-api derives it (store.FitBand).
export type FitBand = "strong" | "good" | "moderate" | "weak";

export type Situation = "" | "exploring" | "immediate" | "reentry" | "growth";

export interface Intent {
  roles: string[] | null;
  designation: string;
  locations: string[] | null;
  salary_min: number;
  salary_max: number;
  currency: string;
  company_types: string[] | null;
  situation: Situation;
}

const BASE = process.env.CORE_API_URL ?? "http://127.0.0.1:8081";

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

const QUIZ_BASE = process.env.QUIZ_API_URL ?? "http://127.0.0.1:8084";

function call<T>(path: string, init?: RequestInit): Promise<T> {
  return request<T>(BASE, "core-api", path, init);
}

/** The quiz service (ADR 0008). */
function quizCall<T>(path: string, init?: RequestInit): Promise<T> {
  return request<T>(QUIZ_BASE, "The quiz service", path, init);
}

async function request<T>(base: string, name: string, path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(base + path, {
      ...init,
      cache: "no-store",
      headers: { "Content-Type": "application/json", ...init?.headers },
      signal: AbortSignal.timeout(5000),
      ...(init?.signal ? { signal: init.signal } : {}),
    });
  } catch {
    throw new ApiError(503, `${name} is not reachable`);
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error ?? res.statusText);
  }
  return res.status === 204 ? (undefined as T) : res.json();
}

export type QuizKind = "mcq" | "subjective" | "voice";
export type QuizTopic = "ai_news" | "pm_concept" | "org_news";

export interface QuizQuestion {
  id: number;
  position: number;
  kind: QuizKind;
  topic: QuizTopic;
  prompt: string;
  options: string[];
  answer: number | null; // MCQ: shown once answered
  explanation: string;
  source_title: string;
  source_url: string;
  response: {
    choice: number | null;
    text: string;
    audio_file: string;
    transcript: string;
    feedback: string;
    score: number | null;
    correct: boolean | null;
    answered_at: string;
    feedback_at: string | null;
  } | null;
}

export interface QuizPage {
  day: string;
  quiz: { day: string; model: string; created_at: string; questions: QuizQuestion[] } | null;
  queued_at: string | null;
  history: { day: string; answered: number; correct: number; total: number }[];
}

export interface TimelineStep {
  stage: "applied" | "shortlisted" | "assessment" | "interview" | "offer" | "rejected";
  round: number;
  label: string;
  at: string;
  days_from_prev: number | null;
  source: "email" | "manual";
}

export interface AppAnalysis extends Application {
  open: boolean;
  stage_since: string;
  days_in_stage: number;
  next_move: { title: string; detail: string; urgent: boolean };
  timeline: TimelineStep[];
  outcome: "" | "offer" | "rejected";
  days_to_outcome: number | null;
  interview_rounds: number;
  fit: { total: number; band: FitBand; verdict: string; created_at: string } | null;
  posting_url: string;
}

export interface DurationStat {
  key: string;
  label: string;
  avg_days: number;
  median_days: number;
  n: number;
}

export interface AnalysisReport {
  applications: AppAnalysis[];
  stage_dwell: DurationStat[];
  outcomes: DurationStat[];
  funnel: {
    applied: number;
    responded: number;
    shortlisted: number;
    interviewed: number;
    offers: number;
    rejected: number;
    response_rate: number;
  };
}

export type FactorKey = "experience" | "location" | "seniority" | "role_alignment" | "interest_intent";

export interface InterviewNote { id: number; round: number; body: string; created_at: string }

export interface InterviewReview {
  chance: "low" | "medium" | "high";
  summary: string;
  signals: string[];
  concerns: string[];
  next_steps: string[];
  model: string;
  created_at: string;
}

export interface PrepQuestion { company: string; role: string; question: string; why: string; approach: string }

export interface AppDetail {
  interview_notes: InterviewNote[];
  interview_review: InterviewReview | null;
  review_requested_at: string | null;
  posting: {
    status: "found" | "not_found";
    source: string;
    url: string;
    title: string;
    location: string;
    work_mode: string;
    seniority: string;
    min_years: number | null;
    product_context: string;
    hiring_manager_name: string;
    hiring_manager_title: string;
    requirements: string[];
    qualifications: string[];
    responsibilities: string[];
    fetched_at: string;
  } | null;
  fit: {
    total: number;
    band: FitBand;
    verdict: string;
    model: string;
    created_at: string;
    factors: { factor: FactorKey; score: number; rationale: string }[];
    suggestions: string[];
    pipeline: string; // "retrieval=claude-sonnet-4-6;summary=..."
    strengths: string[];
    gaps: string[];
    evidence: { requirement: string; evidence: string; strength: "strong" | "partial" | "missing" }[];
  } | null;
  contacts: { id: number; name: string; email: string; role_hint: string; source: string; last_seen_at: string }[];
  drafts: {
    id: number;
    contact_id: number | null;
    subject: string;
    body: string;
    product_analysis: string;
    experience_points: string[];
    value_adds: string[];
    improvement_ideas: string[];
    model: string;
    created_at: string;
  }[];
  job_links: string[];
  research_requested_at: string | null;
  fit_requested_at: string | null;
}

export interface Experience {
  title: string;
  company: string;
  start_month: string;
  end_month: string;
  location: string;
  highlights: string[];
}

export interface Profile {
  full_name: string;
  headline: string;
  current_title: string;
  current_company: string;
  total_years: number;
  location: string;
  summary: string;
  skills: string[];
  experiences: Experience[];
  updated_at: string | null;
  photo_updated_at?: string | null; // read-only: set by the photo endpoints
}

export interface QuizStats {
  quizzes_taken: number;
  mcq_answered: number;
  mcq_correct: number;
  accuracy: number | null;
  accuracy_7d: number | null;
  written_avg: number | null;
  spoken_avg: number | null;
  streak: number;
  feedback_queued: number;
  days: { day: string; answered: number; correct: number; total: number }[];
  last_answered_at: string | null;
}

export interface NewsItem {
  topic: "ai_news" | "org_news";
  company: string;
  title: string;
  summary: string;
  source_title: string;
  url: string;
  published_on: string;
}

export interface OrgSummary {
  id: number;
  name: string;
  preferred: boolean;
  applications: number;
  open: number;
  responded: number;
  shortlisted: number;
  interviewed: number;
  offers: number;
  rejected: number;
  stage_counts: Record<string, number>;
  first_applied_at: string | null;
  last_activity_at: string | null;
  avg_response_days: number | null;
  diagnosis_suggested: boolean;
  diagnosis_reason: string;
  has_profile: boolean;
  has_diagnosis: boolean;
  /** Something new from the daily quiz (news, leaders, revenue) not opened yet. */
  briefing_unseen: boolean;
}

export interface OrgsReport {
  preferred_mode: "auto" | "manual";
  preferred: OrgSummary[];
  organizations: OrgSummary[];
}

export interface CompanyProfile {
  overview: string;
  interview_angle: string;
  news: { title: string; published_on: string; summary: string; url: string }[];
  products: { name: string; description: string }[];
  leaders: { name: string; title: string }[];
  ratings: { source: "glassdoor" | "gartner_peer_insights"; score: number | null; scale: number; review_count: number | null; url: string; as_of: string }[];
  model: string;
  created_at: string;
}

/** A published figure as written ("$177.6B"), with the period it covers. */
export interface Figure { value: string; period: string; url: string }

/** What Claude learned about the company while building a daily quiz. */
export interface Briefing {
  ceo: string;
  cpo: string;
  revenue_year: Figure;
  revenue_quarter: Figure;
  leaders_url: string;
  news: { day: string; title: string; summary: string; source_title: string; url: string; published_on: string }[];
  model: string;
  updated_at: string;
  seen_at: string | null;
}

export interface OrgDetail extends OrgSummary {
  application_history: AppAnalysis[];
  briefing: Briefing | null;
  research_requested_at: string | null;
  profile: CompanyProfile | null;
  diagnosis: {
    summary: string;
    causes: { text: string; confidence: "high" | "medium" | "low" }[];
    actions: string[];
    model: string;
    created_at: string;
  } | null;
}

export type Lookback = "since_last" | "14d" | "1m" | "2m" | "3m" | "6m" | "12m" | "custom";

export interface Mailbox {
  id: number;
  address: string;
  provider: "yahoo" | "gmail" | "icloud" | "microsoft";
  enabled: boolean;
  credential_saved: boolean;
  last_synced_at: string | null;
  last_status: "never" | "ok" | "error";
  last_error: string;
  last_added: number;
  emails: number;
}

export interface SyncSettings {
  lookback: Lookback;
  since_date: string;
}

export interface SyncRun {
  running: boolean;
  started_at: string | null;
  finished_at: string | null;
  mailboxes: { id: number; address: string; status: "pending" | "scanning" | "done" | "error"; found: number; added: number; since: string; error?: string }[];
  error?: string;
}

const INGEST = process.env.INGEST_URL ?? "http://127.0.0.1:8083";

/** Calls the sync service (mail scanning and credentials). */
async function ingestCall<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(INGEST + path, {
      method,
      cache: "no-store",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${process.env.DZ_INTERNAL_TOKEN ?? ""}` },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(60_000),
    });
  } catch {
    throw new ApiError(503, "The sync service isn't running. Start it with: make ingest-service");
  }
  if (!res.ok) {
    const b = await res.json().catch(() => ({}));
    throw new ApiError(res.status, b.error ?? res.statusText);
  }
  return res.status === 204 ? (undefined as T) : res.json();
}

const AGENT = process.env.AGENT_URL ?? "http://127.0.0.1:8082";

/** Calls the agent service (Claude-powered). Slow: research can take a few minutes. */
async function agentCall<T>(path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(AGENT + path, {
      method: "POST",
      cache: "no-store",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${process.env.DZ_INTERNAL_TOKEN ?? ""}` },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(600_000),
    });
  } catch {
    throw new ApiError(503, "The agent service isn't running. Start it with: make agent");
  }
  if (!res.ok) {
    const b = await res.json().catch(() => ({}));
    throw new ApiError(res.status, b.error ?? res.statusText);
  }
  return res.json();
}

export interface DigestApp { id: number; company: string; role: string; kind?: string }
export interface DayPoint { date: string; applications: number; rejections: number; waiting: number }
export interface Digest {
  refresh_id: number;
  refresh_at: string | null;
  since: string;
  new_applications: DigestApp[];
  responses: DigestApp[];
  trends: {
    apps_this_week: number;
    apps_last_week: number;
    responses_this_week: number;
    rejections_this_week: number;
    response_rate_30d: number;
    waiting: number;
  };
  next_steps: { id: number; company: string; role: string; title: string; detail: string }[];
  suggested: { id: number; name: string; reason: string }[];
  series: DayPoint[];
  text: string;
  model: string;
}

export const api = {
  summary: () => call<Summary>("/api/v1/summary"),
  digest: () => call<Digest>("/api/v1/digest"),
  applications: (f: { stage?: string; q?: string } = {}) => {
    const p = new URLSearchParams();
    if (f.stage) p.set("stage", f.stage);
    if (f.q) p.set("q", f.q);
    const qs = p.toString();
    return call<{ applications: Application[] }>(`/api/v1/applications${qs ? `?${qs}` : ""}`);
  },
  application: (id: number) =>
    call<{ application: Application; events: MailEvent[] }>(`/api/v1/applications/${id}`),
  editApplication: (id: number, fields: { company?: string; role?: string; job_url?: string; rounds_done?: number; next_step?: string }) =>
    call<{ id: number }>(`/api/v1/applications/${id}`, { method: "PATCH", body: JSON.stringify(fields) }),
  setStage: (id: number, stage: Stage | "") =>
    call<void>(`/api/v1/applications/${id}`, { method: "PATCH", body: JSON.stringify({ stage }) }),
  intent: () => call<Intent>("/api/v1/intent"),
  putIntent: (i: Intent) => call<Intent>("/api/v1/intent", { method: "PUT", body: JSON.stringify(i) }),
  analysis: () => call<AnalysisReport>("/api/v1/analysis"),
  detail: (id: number) => call<AppDetail>(`/api/v1/applications/${id}/detail`),
  profile: () => call<Profile>("/api/v1/profile"),
  putProfile: (p: Profile) => call<Profile>("/api/v1/profile", { method: "PUT", body: JSON.stringify(p) }),
  mailboxes: () => call<{ mailboxes: Mailbox[]; settings: SyncSettings }>("/api/v1/mailboxes"),
  addMailbox: (address: string) => call<Mailbox>("/api/v1/mailboxes", { method: "POST", body: JSON.stringify({ address }) }),
  setMailboxEnabled: (id: number, enabled: boolean) =>
    call<void>(`/api/v1/mailboxes/${id}`, { method: "PATCH", body: JSON.stringify({ enabled }) }),
  putSyncSettings: (s: SyncSettings) => call<SyncSettings>("/api/v1/sync-settings", { method: "PUT", body: JSON.stringify(s) }),
  syncStatus: () => ingestCall<SyncRun>("GET", "/v1/sync"),
  startSync: (ids: number[]) => ingestCall<SyncRun>("POST", "/v1/sync", { mailbox_ids: ids }),
  savePassword: (id: number, password: string) => ingestCall<void>("POST", `/v1/mailboxes/${id}/password`, { password }),
  startMicrosoft: (id: number) =>
    ingestCall<{ verification_uri: string; user_code: string; expires_in: number }>("POST", `/v1/mailboxes/${id}/microsoft`),
  microsoftStatus: (id: number) => ingestCall<{ status: "pending" | "connected" | "error"; error?: string }>("GET", `/v1/mailboxes/${id}/microsoft`),
  disconnect: (id: number, forget: boolean) => ingestCall<void>("DELETE", `/v1/mailboxes/${id}${forget ? "?forget=true" : ""}`),
  organizations: () => call<OrgsReport>("/api/v1/organizations"),
  organization: (id: number) => call<OrgDetail>(`/api/v1/organizations/${id}`),
  setPreferred: (names: string[], auto: boolean) =>
    call<OrgsReport>("/api/v1/organizations/preferred", { method: "PUT", body: JSON.stringify({ names, auto }) }),
  requestResearch: (kind: "organizations" | "applications", id: number) =>
    call<{ research_requested_at: string }>(`/api/v1/${kind}/${id}/research`, { method: "POST" }),
  briefingSeen: (id: number) => call<void>(`/api/v1/organizations/${id}/briefing/seen`, { method: "POST" }),
  addInterviewNote: (id: number, note: { round: number; body: string }) =>
    call<InterviewNote>(`/api/v1/applications/${id}/interview-notes`, { method: "POST", body: JSON.stringify(note) }),
  prep: () => quizCall<{ day: string; items: PrepQuestion[] }>("/api/v1/prep"),
  requestFit: (id: number) =>
    call<{ research_requested_at: string }>(`/api/v1/applications/${id}/fit-request`, { method: "POST" }),
  quizStats: () => quizCall<QuizStats>("/api/v1/stats"),
  news: () => quizCall<{ day: string; items: NewsItem[] }>("/api/v1/news"),
  uploadPhoto: (photo: Blob) =>
    call<Profile>("/api/v1/profile/photo", {
      method: "PUT", body: photo, headers: { "Content-Type": photo.type }, signal: AbortSignal.timeout(20000),
    }),
  deletePhoto: () => call<void>("/api/v1/profile/photo", { method: "DELETE" }),
  photo: async (): Promise<Response> => {
    const res = await fetch(`${BASE}/api/v1/profile/photo`, { cache: "no-store", signal: AbortSignal.timeout(10000) });
    if (!res.ok) throw new ApiError(res.status, "no photo");
    return res;
  },
  quiz: (day?: string) => quizCall<QuizPage>(`/api/v1/quiz${day ? `?day=${encodeURIComponent(day)}` : ""}`),
  answerQuestion: (id: number, body: { choice?: number; text?: string }) =>
    quizCall<QuizQuestion>(`/api/v1/quiz/questions/${id}/answer`, { method: "POST", body: JSON.stringify(body) }),
  uploadVoice: (id: number, audio: Blob) =>
    quizCall<QuizQuestion>(`/api/v1/quiz/questions/${id}/voice`, {
      method: "PUT", body: audio, headers: { "Content-Type": audio.type }, signal: AbortSignal.timeout(30000),
    }),
  voice: async (id: number): Promise<Response> => {
    const res = await fetch(`${QUIZ_BASE}/api/v1/quiz/questions/${id}/voice`, { cache: "no-store", signal: AbortSignal.timeout(10000) });
    if (!res.ok) throw new ApiError(res.status, "recording not found");
    return res;
  },
  runDiagnosis: (id: number) => agentCall<unknown>(`/v1/organizations/${id}/diagnosis`),
  runFit: (id: number) => agentCall<unknown>(`/v1/applications/${id}/fit`),
  runOutreach: (id: number, contactId: number | null) =>
    agentCall<unknown>(`/v1/applications/${id}/outreach`, { contact_id: contactId }),
};
