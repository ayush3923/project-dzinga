import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";

vi.mock("server-only", () => ({})); // api.ts is server-only; the test runs it directly

const org = (id: number, name: string) => ({ id, name, preferred: false, applications: 1, open: 1, responded: 0, shortlisted: 0,
  interviewed: 0, offers: 0, rejected: 0, stage_counts: {}, first_applied_at: null, last_activity_at: null,
  avg_response_days: null, diagnosis_suggested: false, diagnosis_reason: "" });

function respond(routes: Record<string, unknown | Error>) {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const hit = Object.entries(routes).find(([path]) => url.endsWith(path));
    if (!hit || hit[1] instanceof Error) throw new TypeError("fetch failed");
    return new Response(JSON.stringify(hit[1]), { status: 200, headers: { "Content-Type": "application/json" } });
  }));
}

afterEach(() => vi.unstubAllGlobals());

describe("organizations", () => {
  it("adds research flags from the startoff service", async () => {
    respond({
      ":8085/api/v1/organizations": { preferred_mode: "auto", preferred: [org(1, "Acme")], organizations: [org(1, "Acme"), org(2, "Beta")] },
      "/api/v1/organizations/flags": { flags: [{ organization_id: 1, has_profile: true, has_diagnosis: true, briefing_unseen: false, diagnosis_unseen: true }] },
    });
    const rep = await api.organizations();
    expect(rep.preferred[0].diagnosis_unseen).toBe(true);
    expect(rep.organizations[0]).toMatchObject({ has_profile: true, has_diagnosis: true, diagnosis_unseen: true });
    expect(rep.organizations[1]).toMatchObject({ has_profile: false, has_diagnosis: false, briefing_unseen: false, diagnosis_unseen: false });
  });

  it("still lists organizations when the startoff service is down", async () => {
    respond({
      ":8085/api/v1/organizations": { preferred_mode: "auto", preferred: [], organizations: [org(1, "Acme")] },
      "/api/v1/organizations/flags": new Error("down"),
    });
    const rep = await api.organizations();
    expect(rep.organizations).toHaveLength(1);
    expect(rep.organizations[0].has_diagnosis).toBe(false);
  });
});

describe("organization", () => {
  it("joins the organization with its research and history counts", async () => {
    respond({
      ":8085/api/v1/organizations/7": { ...org(7, "Acme"), application_history: [] },
      ":8086/api/v1/organizations/7": {
        organization_id: 7, has_profile: false, has_diagnosis: true, briefing_unseen: false, diagnosis_unseen: true,
        profile: null, profile_versions: 0, briefing: null, news_count: 0,
        diagnosis: { summary: "Too early.", causes: [], actions: [], model: "m", created_at: "2026-11-20T02:00:00Z" },
        earlier_diagnoses: [{ summary: "Silent.", causes: [], actions: [], model: "m", created_at: "2026-11-01T02:00:00Z" }],
        research_requested_at: null, diagnosis_requested_at: null,
      },
    });
    const d = await api.organization(7);
    expect(d.id).toBe(7);
    expect(d.name).toBe("Acme");
    expect(d.diagnosis?.summary).toBe("Too early.");
    expect(d.earlier_diagnoses).toHaveLength(1);
  });

  it("shows the organization without research when the startoff service is down", async () => {
    respond({ ":8085/api/v1/organizations/7": { ...org(7, "Acme"), application_history: [] }, ":8086/api/v1/organizations/7": new Error("down") });
    const d = await api.organization(7);
    expect(d).toMatchObject({ id: 7, profile: null, diagnosis: null, earlier_diagnoses: [], has_diagnosis: false });
  });
});
