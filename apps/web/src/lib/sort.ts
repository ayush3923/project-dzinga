// Sorting for the Analysis and Organizations pages. Pure, so it is unit-tested.

import { STAGES } from "./format";

export const SORT_KEYS = ["company", "role", "stage", "days", "fit", "next", "applied"] as const;
export type SortKey = (typeof SORT_KEYS)[number];
export type SortDir = "asc" | "desc";

export const SORT_LABELS: Record<SortKey, string> = {
  company: "Company", role: "Role", stage: "Stage", days: "Days in stage", fit: "Fit", next: "Next move", applied: "Applied",
};

/** The direction a column sorts in when first clicked: biggest first for numbers and dates. */
export const FIRST_DIR: Record<SortKey, SortDir> = {
  company: "asc", role: "asc", stage: "asc", days: "desc", fit: "desc", next: "asc", applied: "desc",
};

export function isSortKey(v: unknown): v is SortKey {
  return typeof v === "string" && (SORT_KEYS as readonly string[]).includes(v);
}

export interface Sortable {
  company: string;
  role: string;
  stage: string;
  days_in_stage: number;
  applied_at: string;
  fit: { total: number } | null;
  open: boolean;
  next_move: { title: string; urgent: boolean };
}

const text = new Intl.Collator("en", { sensitivity: "base", numeric: true });

/**
 * Sorts a copy of the list. Missing values (no fit score, no role) go last in
 * both directions; ties fall back to company name so the order is stable.
 */
export function sortApps<T extends Sortable>(list: T[], key: SortKey, dir: SortDir): T[] {
  const sign = dir === "asc" ? 1 : -1;
  const stageRank = (s: string) => (STAGES as readonly string[]).indexOf(s);
  const value = (a: T): string | number | null => {
    switch (key) {
      case "company": return a.company;
      case "role": return a.role || null;
      case "stage": return stageRank(a.stage);
      case "days": return a.days_in_stage;
      case "fit": return a.fit ? a.fit.total : null;
      case "next": return a.open ? `${a.next_move.urgent ? 0 : 1}${a.next_move.title}` : null;
      case "applied": return Date.parse(a.applied_at);
    }
  };
  return [...list].sort((x, y) => {
    const a = value(x), b = value(y);
    if (a === null || b === null) {
      if (a !== b) return a === null ? 1 : -1;
    } else {
      const c = typeof a === "number" && typeof b === "number" ? a - b : text.compare(String(a), String(b));
      if (c !== 0) return sign * c;
    }
    return text.compare(x.company, y.company);
  });
}

// ---------- Organizations page ----------

export const ORG_SORT_KEYS = ["name", "applications", "outcomes", "first", "response"] as const;
export type OrgSortKey = (typeof ORG_SORT_KEYS)[number];

export const ORG_SORT_LABELS: Record<OrgSortKey, string> = {
  name: "Organization", applications: "Applications", outcomes: "Outcomes", first: "First applied", response: "Avg. response",
};

/** Biggest first for counts and outcomes, A–Z for names, oldest first for dates, fastest first for replies. */
export const ORG_FIRST_DIR: Record<OrgSortKey, SortDir> = {
  name: "asc", applications: "desc", outcomes: "desc", first: "asc", response: "asc",
};

export function isOrgSortKey(v: unknown): v is OrgSortKey {
  return typeof v === "string" && (ORG_SORT_KEYS as readonly string[]).includes(v);
}

export interface OrgSortable {
  name: string;
  applications: number;
  open: number;
  shortlisted: number;
  interviewed: number;
  offers: number;
  rejected: number;
  first_applied_at: string | null;
  avg_response_days: number | null;
}

/**
 * Sorts a copy of the list. Outcomes rank by how far things got: offers, then
 * interviews, then shortlists, then fewer rejections. Missing values (never
 * applied, no reply) go last in both directions; ties fall back to the name.
 */
export function sortOrgs<T extends OrgSortable>(list: T[], key: OrgSortKey, dir: SortDir): T[] {
  const sign = dir === "asc" ? 1 : -1;
  const value = (o: T): string | number[] | null => {
    switch (key) {
      case "name": return o.name;
      case "applications": return [o.applications, o.open];
      case "outcomes": return o.applications === 0 ? null : [o.offers, o.interviewed, o.shortlisted, -o.rejected];
      case "first": return o.first_applied_at ? [Date.parse(o.first_applied_at)] : null;
      case "response": return o.avg_response_days === null ? null : [o.avg_response_days];
    }
  };
  const compare = (a: string | number[], b: string | number[]) => {
    if (typeof a === "string" || typeof b === "string") return text.compare(String(a), String(b));
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return a[i] - b[i];
    return 0;
  };
  return [...list].sort((x, y) => {
    const a = value(x), b = value(y);
    if (a === null || b === null) {
      if (a !== b) return a === null ? 1 : -1;
    } else {
      const c = compare(a, b);
      if (c !== 0) return sign * c;
    }
    return text.compare(x.name, y.name);
  });
}
