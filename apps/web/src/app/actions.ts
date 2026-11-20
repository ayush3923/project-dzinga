"use server";

// Server Functions are public HTTP endpoints, so every input is re-validated here even
// though the UI only sends valid values.
import { refresh } from "next/cache";
import { ApiError, api, type Experience, type Intent, type Lookback, type Profile, type QuizQuestion, type Situation, type SyncRun } from "@/lib/api";
import { isStage } from "@/lib/format";

export async function setStageAction(id: number, stage: string): Promise<{ error?: string }> {
  if (!Number.isInteger(id) || id <= 0) return { error: "Invalid application." };
  if (stage !== "" && !isStage(stage)) return { error: "Invalid stage." };
  try {
    await api.setStage(id, stage as Parameters<typeof api.setStage>[1]);
  } catch {
    return { error: "Couldn’t save. Is core-api running?" };
  }
  refresh();
  return {};
}

const SITUATIONS: Situation[] = ["", "exploring", "immediate", "reentry", "growth"];
const COMPANY_TYPES = ["startup", "scaleup", "enterprise", "mnc", "public", "nonprofit"];

const list = (v: unknown, max = 10) =>
  Array.isArray(v)
    ? v.filter((x): x is string => typeof x === "string").map((s) => s.trim().slice(0, 100)).filter(Boolean).slice(0, max)
    : [];

export async function saveIntentAction(input: unknown): Promise<{ error?: string }> {
  if (typeof input !== "object" || input === null) return { error: "Invalid input." };
  const i = input as Record<string, unknown>;
  const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) && v >= 0 ? Math.floor(v) : 0);
  const intent: Intent = {
    roles: list(i.roles),
    designation: typeof i.designation === "string" ? i.designation.trim().slice(0, 100) : "",
    locations: list(i.locations),
    salary_min: num(i.salary_min),
    salary_max: num(i.salary_max),
    currency: i.currency === "USD" ? "USD" : "INR",
    company_types: list(i.company_types).filter((c) => COMPANY_TYPES.includes(c)),
    situation: SITUATIONS.includes(i.situation as Situation) ? (i.situation as Situation) : "",
  };
  if (intent.salary_max && intent.salary_min > intent.salary_max) {
    return { error: "Minimum salary is above the maximum." };
  }
  if (intent.currency === "USD" ? intent.salary_max > 10_000_000 : intent.salary_max > 500_000_000) {
    return { error: intent.currency === "USD" ? "Enter thousands of dollars, e.g. 180." : "Enter lakhs per annum, e.g. 60, not rupees." };
  }
  try {
    await api.putIntent(intent);
  } catch {
    return { error: "Couldn’t save. Is core-api running?" };
  }
  refresh();
  return {};
}

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

/** Runs the Claude fit analysis for one application (takes up to a few minutes). */
export async function runFitAction(id: number): Promise<{ error?: string }> {
  if (!Number.isInteger(id) || id <= 0) return { error: "Invalid application." };
  try {
    await api.runFit(id);
  } catch (e) {
    return { error: message(e, "The fit analysis failed.") };
  }
  refresh();
  return {};
}

/** Drafts a cold email on request. Nothing is sent. */
/** Queues a cold email for Claude, for a contact or a recipient the user typed. */
export async function draftEmailAction(id: number, contactId: number | null, recipient: string): Promise<{ error?: string }> {
  if (!Number.isInteger(id) || id <= 0) return { error: "Invalid application." };
  if (contactId !== null && (!Number.isInteger(contactId) || contactId <= 0)) return { error: "Invalid contact." };
  const who = typeof recipient === "string" ? recipient.trim().slice(0, 120) : "";
  try {
    await api.requestOutreach(id, contactId, who);
  } catch (e) {
    return { error: message(e, "Couldn’t queue the email.") };
  }
  refresh();
  return {};
}

/** Opening a new draft clears its tile's "ready" icon. */
export async function draftsSeenAction(id: unknown): Promise<void> {
  if (!validId(id)) return;
  try {
    await api.draftsSeen(id);
  } catch {
    return; // the icon simply stays until next time
  }
  refresh();
}

const text = (v: unknown, max: number) => (typeof v === "string" ? v.trim().slice(0, max) : "");
const month = (v: unknown) => (typeof v === "string" && /^\d{4}-(0[1-9]|1[0-2])$/.test(v) ? v : "");

export async function saveProfileAction(input: unknown): Promise<{ error?: string }> {
  if (typeof input !== "object" || input === null) return { error: "Invalid input." };
  const i = input as Record<string, unknown>;
  const years = typeof i.total_years === "number" && Number.isFinite(i.total_years) ? i.total_years : 0;
  const exps: Experience[] = (Array.isArray(i.experiences) ? i.experiences : []).slice(0, 30).map((raw) => {
    const e = (raw ?? {}) as Record<string, unknown>;
    return {
      title: text(e.title, 150),
      company: text(e.company, 150),
      start_month: month(e.start_month),
      end_month: month(e.end_month),
      location: text(e.location, 100),
      highlights: (Array.isArray(e.highlights) ? e.highlights : []).map((h) => text(h, 600)).filter(Boolean).slice(0, 12),
    };
  }).filter((e) => e.title && e.company);
  const profile: Profile = {
    full_name: text(i.full_name, 120),
    headline: text(i.headline, 200),
    current_title: text(i.current_title, 150),
    current_company: text(i.current_company, 150),
    total_years: Math.max(0, Math.min(60, years)),
    location: text(i.location, 100),
    summary: text(i.summary, 3000),
    skills: (Array.isArray(i.skills) ? i.skills : []).map((s) => text(s, 80)).filter(Boolean).slice(0, 60),
    experiences: exps,
    updated_at: null,
  };
  try {
    await api.putProfile(profile);
  } catch (e) {
    return { error: message(e, "Couldn’t save the profile.") };
  }
  refresh();
  return {};
}

/** Replaces the preferred organizations, or returns to "top 3 by applications". */
export async function savePreferredAction(names: unknown, auto: boolean): Promise<{ error?: string }> {
  const list = (Array.isArray(names) ? names : []).map((n) => text(n, 120)).filter((n) => /[\p{L}\p{N}]/u.test(n)).slice(0, 20);
  try {
    await api.setPreferred(auto ? [] : list, auto === true);
  } catch (e) {
    return { error: message(e, "Couldn’t save your preferred organizations.") };
  }
  refresh();
  return {};
}

/** Queues web research (a company profile, or finding a job posting) for Claude in the app. */
export async function requestResearchAction(kind: "organizations" | "applications", id: number): Promise<{ error?: string }> {
  if ((kind !== "organizations" && kind !== "applications") || !Number.isInteger(id) || id <= 0) return { error: "Invalid request." };
  try {
    await api.requestResearch(kind, id);
  } catch (e) {
    return { error: message(e, "Couldn’t queue the research.") };
  }
  refresh();
  return {};
}

export async function diagnoseAction(id: number): Promise<{ error?: string }> {
  if (!Number.isInteger(id) || id <= 0) return { error: "Invalid organization." };
  try {
    await api.requestDiagnosis(id);
  } catch (e) {
    return { error: message(e, "Couldn’t queue the diagnosis.") };
  }
  refresh();
  return {};
}

// ---------- Mailboxes and sync ----------

const validId = (id: unknown): id is number => typeof id === "number" && Number.isInteger(id) && id > 0;

export type SyncSummary = { run: SyncRun | null; lastSyncedAt: string | null; connected: number; selected: number; error?: string };

/** Sync status for the sidebar: the current run plus when anything last synced. */
export async function syncStatusAction(): Promise<SyncSummary> {
  let lastSyncedAt: string | null = null;
  let connected = 0;
  let selected = 0;
  try {
    const { mailboxes } = await api.mailboxes();
    for (const m of mailboxes) {
      if (m.credential_saved) connected++;
      if (m.credential_saved && m.enabled) selected++;
      if (m.last_synced_at && (!lastSyncedAt || m.last_synced_at > lastSyncedAt)) lastSyncedAt = m.last_synced_at;
    }
  } catch (e) {
    return { run: null, lastSyncedAt, connected, selected, error: message(e, "core-api is not reachable") };
  }
  try {
    return { run: await api.syncStatus(), lastSyncedAt, connected, selected };
  } catch (e) {
    return { run: null, lastSyncedAt, connected, selected, error: message(e, "The sync service isn't running.") };
  }
}

export async function startSyncAction(ids: unknown): Promise<{ error?: string }> {
  const list = (Array.isArray(ids) ? ids : []).filter(validId).slice(0, 20);
  try {
    // Every mailbox: the day's run (startoff), which also requests the quiz and queues diagnoses.
    if (list.length === 0) await api.startRun();
    else await api.startSync(list);
  } catch (e) {
    return { error: message(e, "Couldn’t start the sync.") };
  }
  return {};
}

/** Called when a sync finishes, so every page shows the new data. */
export async function refreshAfterSyncAction(): Promise<void> {
  refresh();
}

export async function addMailboxAction(address: unknown): Promise<{ error?: string }> {
  try {
    await api.addMailbox(text(address, 254));
  } catch (e) {
    return { error: message(e, "Couldn’t add the mailbox.") };
  }
  refresh();
  return {};
}

export async function setMailboxEnabledAction(id: unknown, enabled: unknown): Promise<{ error?: string }> {
  if (!validId(id) || typeof enabled !== "boolean") return { error: "Invalid request." };
  try {
    await api.setMailboxEnabled(id, enabled);
  } catch (e) {
    return { error: message(e, "Couldn’t update the mailbox.") };
  }
  refresh();
  return {};
}

/** Checks an app password with the mail server, then stores it in the Keychain. */
export async function saveMailboxPasswordAction(id: unknown, password: unknown): Promise<{ error?: string }> {
  if (!validId(id) || typeof password !== "string" || password.trim() === "" || password.length > 256) {
    return { error: "Enter the app password." };
  }
  try {
    await api.savePassword(id, password);
  } catch (e) {
    return { error: message(e, "Couldn’t save the password.") };
  }
  refresh();
  return {};
}

export async function startMicrosoftAction(id: unknown): Promise<{ error?: string; uri?: string; code?: string }> {
  if (!validId(id)) return { error: "Invalid mailbox." };
  try {
    const r = await api.startMicrosoft(id);
    return { uri: r.verification_uri, code: r.user_code };
  } catch (e) {
    return { error: message(e, "Couldn’t start the Microsoft sign-in.") };
  }
}

export async function microsoftStatusAction(id: unknown): Promise<{ status: "pending" | "connected" | "error"; error?: string }> {
  if (!validId(id)) return { status: "error", error: "Invalid mailbox." };
  try {
    const s = await api.microsoftStatus(id);
    if (s.status !== "pending") refresh();
    return s;
  } catch (e) {
    return { status: "error", error: message(e, "Couldn’t check the sign-in.") };
  }
}

export async function disconnectMailboxAction(id: unknown, forget: unknown): Promise<{ error?: string }> {
  if (!validId(id)) return { error: "Invalid mailbox." };
  try {
    await api.disconnect(id, forget === true);
  } catch (e) {
    return { error: message(e, "Couldn’t disconnect.") };
  }
  refresh();
  return {};
}

const LOOKBACKS: Lookback[] = ["since_last", "14d", "1m", "2m", "3m", "6m", "12m", "custom"];

export async function saveSyncSettingsAction(lookback: unknown, sinceDate: unknown): Promise<{ error?: string }> {
  if (!LOOKBACKS.includes(lookback as Lookback)) return { error: "Choose how far back to scan." };
  const date = typeof sinceDate === "string" && /^\d{4}-\d{2}-\d{2}$/.test(sinceDate) ? sinceDate : "";
  if (lookback === "custom" && !date) return { error: "Pick a start date." };
  try {
    await api.putSyncSettings({ lookback: lookback as Lookback, since_date: date });
  } catch (e) {
    return { error: message(e, "Couldn’t save the sync settings.") };
  }
  refresh();
  return {};
}

/** Saves corrections to an application. Returns its id, which changes if it was merged. */
export async function saveRoundsAction(id: unknown, rounds: unknown, next: unknown): Promise<{ error?: string }> {
  if (!validId(id)) return { error: "Invalid application." };
  if (typeof rounds !== "number" || !Number.isInteger(rounds) || rounds < 0 || rounds > 20) {
    return { error: "Rounds done must be between 0 and 20." };
  }
  if (typeof next !== "string" || next.length > 120 || /\p{Cc}/u.test(next)) {
    return { error: "Keep the next step to one line of at most 120 characters." };
  }
  try {
    await api.editApplication(id, { rounds_done: rounds, next_step: next.trim() });
  } catch (e) {
    return { error: message(e, "Couldn’t save the changes.") };
  }
  refresh();
  return {};
}

export async function editApplicationAction(
  id: unknown, company: unknown, role: unknown, jobUrl: unknown,
): Promise<{ id?: number; error?: string }> {
  if (!validId(id)) return { error: "Invalid application." };
  const fields: { company?: string; role?: string; job_url?: string } = {};
  if (typeof company === "string") fields.company = company.trim().slice(0, 120);
  if (typeof role === "string") fields.role = role.trim().slice(0, 200);
  if (typeof jobUrl === "string") {
    const u = jobUrl.trim();
    if (u && !/^https:\/\/[^\s]+$/.test(u)) return { error: "Enter a public https link to the job description." };
    fields.job_url = u.slice(0, 2000);
  }
  if (fields.company === "") return { error: "Enter the organization’s name." };
  try {
    const r = await api.editApplication(id, fields);
    refresh();
    return { id: r.id };
  } catch (e) {
    return { error: message(e, "Couldn’t save the changes.") };
  }
}

/** Queues an Opus fit score for Claude's next run (see docs/runbooks/claude-queue.md). */
export async function requestFitAction(id: unknown): Promise<{ error?: string }> {
  if (!validId(id)) return { error: "Invalid application." };
  try {
    await api.requestFit(id);
  } catch (e) {
    return { error: message(e, "Couldn’t queue the fit score.") };
  }
  refresh();
  return {};
}

/** Saves the user's notes on an interview round and queues Claude's review of them. */
export async function addInterviewNoteAction(id: unknown, round: unknown, body: unknown): Promise<{ error?: string }> {
  if (!validId(id)) return { error: "Invalid application." };
  if (typeof round !== "number" || !Number.isInteger(round) || round < 0 || round > 20) {
    return { error: "Round must be between 0 and 20." };
  }
  const b = typeof body === "string" ? body.trim() : "";
  if (b.length < 10 || b.length > 4000) return { error: "Write 10–4000 characters about the interview." };
  try {
    await api.addInterviewNote(id, { round, body: b });
  } catch (e) {
    return { error: message(e, "Couldn’t save your notes.") };
  }
  refresh();
  return {};
}

/** Clears an organization's "something new" flag once its drawer is opened. */
export async function organizationSeenAction(id: unknown): Promise<void> {
  if (!validId(id)) return;
  try {
    await api.organizationSeen(id);
  } catch {
    return; // the flag simply stays until next time
  }
  refresh();
}

/** Answers a quiz question: an option for an MCQ, or text for the written question. */
export async function answerQuizAction(id: unknown, answer: unknown): Promise<{ question?: QuizQuestion; error?: string }> {
  if (!validId(id)) return { error: "Invalid question." };
  let body: { choice?: number; text?: string };
  if (typeof answer === "number" && Number.isInteger(answer) && answer >= 0 && answer < 10) body = { choice: answer };
  else if (typeof answer === "string" && answer.trim() !== "" && answer.length <= 4000) body = { text: answer.trim() };
  else return { error: "Write an answer of at most 4,000 characters." };
  try {
    const question = await api.answerQuestion(id, body);
    refresh();
    return { question };
  } catch (e) {
    return { error: message(e, "Couldn’t save the answer.") };
  }
}

const AUDIO_TYPES = /^audio\/(webm|ogg|mp4|x-m4a)(;.*)?$/;

/** Saves a recorded answer to the voice question. core-api checks the file's contents. */
export async function uploadVoiceAction(form: FormData): Promise<{ question?: QuizQuestion; error?: string }> {
  const id = Number(form.get("id"));
  const audio = form.get("audio");
  if (!validId(id)) return { error: "Invalid question." };
  if (!(audio instanceof Blob) || !AUDIO_TYPES.test(audio.type)) return { error: "This browser’s recording format isn’t supported." };
  if (audio.size > 8 * 1024 * 1024) return { error: "The recording is too long. Keep it under about four minutes." };
  try {
    const question = await api.uploadVoice(id, audio);
    refresh();
    return { question };
  } catch (e) {
    return { error: message(e, "Couldn’t save the recording.") };
  }
}

/** Saves the profile photo. The browser has already shrunk it; core-api re-encodes it and drops its metadata. */
export async function uploadPhotoAction(form: FormData): Promise<{ error?: string }> {
  const photo = form.get("photo");
  if (!(photo instanceof Blob) || !/^image\/(jpeg|png)$/.test(photo.type)) return { error: "Choose a JPEG or PNG photo." };
  if (photo.size > 5 * 1024 * 1024) return { error: "The photo is too large (5 MB at most)." };
  try {
    await api.uploadPhoto(photo);
  } catch (e) {
    return { error: message(e, "Couldn’t save the photo.") };
  }
  refresh();
  return {};
}

export async function deletePhotoAction(): Promise<{ error?: string }> {
  try {
    await api.deletePhoto();
  } catch (e) {
    return { error: message(e, "Couldn’t remove the photo.") };
  }
  refresh();
  return {};
}
