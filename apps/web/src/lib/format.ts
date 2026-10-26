// Pure helpers shared by server and client components.

export const STAGES = ["applied", "assessment", "interview", "offer", "rejected"] as const;
export type StageKey = (typeof STAGES)[number];

export const STAGE_META: Record<StageKey | "update", { label: string; verb: string }> = {
  applied: { label: "Applied", verb: "Application sent" },
  assessment: { label: "Assessment", verb: "Assessment received" },
  interview: { label: "Interviewing", verb: "Interview stage" },
  offer: { label: "Offer", verb: "Offer received" },
  rejected: { label: "Closed", verb: "Not moving forward" },
  update: { label: "Update", verb: "Status update" },
};

export function isStage(s: unknown): s is StageKey {
  return typeof s === "string" && (STAGES as readonly string[]).includes(s);
}

const DAY = 86_400_000;

export function daysBetween(fromISO: string, now: Date): number {
  return Math.max(0, Math.floor((now.getTime() - new Date(fromISO).getTime()) / DAY));
}

export function relativeDays(iso: string, now: Date): string {
  const d = daysBetween(iso, now);
  if (d === 0) return "today";
  if (d === 1) return "yesterday";
  if (d < 7) return `${d} days ago`;
  if (d < 30) return `${Math.floor(d / 7)} wk ago`;
  if (d < 365) return `${Math.floor(d / 30)} mo ago`;
  return `${Math.floor(d / 365)} yr ago`;
}

export function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" });
}

export function greeting(now: Date): string {
  const h = now.getHours();
  return h < 12 ? "Good morning" : h < 17 ? "Good afternoon" : "Good evening";
}

/** One plain-English sentence describing the pipeline: the heart of the Today view. */
export function brief(byStage: Record<StageKey, number>, quiet: number): string {
  const parts: string[] = [];
  const n = (c: number, one: string, many: string) => `${c} ${c === 1 ? one : many}`;
  if (byStage.offer) parts.push(n(byStage.offer, "offer to decide on", "offers to decide on"));
  if (byStage.interview) parts.push(n(byStage.interview, "process in interviews", "processes in interviews"));
  if (byStage.assessment) parts.push(n(byStage.assessment, "assessment pending", "assessments pending"));
  if (quiet) parts.push(n(quiet, "application that has gone quiet", "applications that have gone quiet"));
  if (parts.length === 0) {
    const open = byStage.applied;
    return open
      ? `${n(open, "application is", "applications are")} in flight. Nothing needs you right now.`
      : "Nothing needs you right now.";
  }
  const last = parts.pop()!;
  return `You have ${parts.length ? `${parts.join(", ")} and ${last}` : last}.`;
}

export function initials(company: string): string {
  const words = company.replace(/[^\p{L}\p{N} ]/gu, "").split(/\s+/).filter(Boolean);
  return (words.length > 1 ? words[0][0] + words[1][0] : (words[0] ?? "?").slice(0, 2)).toUpperCase();
}

const MODEL_NAMES: Record<string, string> = {
  "claude-opus-5-5": "Opus 5.5",
  "claude-sonnet-5": "Sonnet 5",
  "claude-sonnet-4-6": "Sonnet 4.6",
  rules: "rules (no AI)",
  template: "template (no AI)",
};

/** "retrieval=claude-sonnet-4-6;summary=…" → [{task: "Retrieval", model: "Sonnet 4.6"}, …] */
export function parsePipeline(p: string): { task: string; model: string }[] {
  return p
    .split(";")
    .map((pair) => pair.split("="))
    .filter((kv) => kv.length === 2 && kv[0] && kv[1])
    .map(([task, model]) => ({ task: task[0].toUpperCase() + task.slice(1), model: MODEL_NAMES[model] ?? model }));
}

export type Provider = "yahoo" | "outlook" | "gmail" | "icloud" | "other";

/** Which mail provider an address belongs to, for the small mailbox badge. */
export function providerOf(address: string): { id: Provider; label: string; letter: string } {
  const domain = address.toLowerCase().split("@")[1] ?? "";
  const base = domain.split(".")[0];
  if (base === "yahoo" || base === "ymail" || base === "rocketmail") return { id: "yahoo", label: "Yahoo Mail", letter: "Y" };
  if (["outlook", "live", "hotmail", "msn"].includes(base)) return { id: "outlook", label: "Outlook", letter: "O" };
  if (base === "gmail" || base === "googlemail") return { id: "gmail", label: "Gmail", letter: "G" };
  if (["icloud", "me", "mac"].includes(base)) return { id: "icloud", label: "iCloud Mail", letter: "i" };
  return { id: "other", label: "Email", letter: "@" };
}
