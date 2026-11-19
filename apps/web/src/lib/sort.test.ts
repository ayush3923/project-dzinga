import { describe, expect, it } from "vitest";
import { isOrgSortKey, isSortKey, sortApps, sortOrgs, type OrgSortable, type Sortable } from "./sort";

const app = (company: string, o: Partial<Sortable> = {}): Sortable => ({
  company, role: "PM", stage: "applied", days_in_stage: 1, applied_at: "2026-11-01T00:00:00Z", fit: null, open: true,
  next_move: { title: "Wait", urgent: false }, ...o,
});
const names = (l: Sortable[]) => l.map((a) => a.company).join(",");

describe("sortApps", () => {
  const list = [
    app("beta", { fit: { total: 60 }, days_in_stage: 9, stage: "interview", applied_at: "2026-11-10T00:00:00Z" }),
    app("Alpha", { fit: null, days_in_stage: 3, stage: "rejected", open: false, role: "" }),
    app("gamma", { fit: { total: 82 }, days_in_stage: 3, next_move: { title: "Follow up", urgent: true } }),
  ];
  it("sorts text case-insensitively both ways", () => {
    expect(names(sortApps(list, "company", "asc"))).toBe("Alpha,beta,gamma");
    expect(names(sortApps(list, "company", "desc"))).toBe("gamma,beta,Alpha");
  });
  it("keeps missing fit scores and roles last in both directions", () => {
    expect(names(sortApps(list, "fit", "desc"))).toBe("gamma,beta,Alpha");
    expect(names(sortApps(list, "fit", "asc"))).toBe("beta,gamma,Alpha");
    expect(names(sortApps(list, "role", "desc"))).toBe("beta,gamma,Alpha");
  });
  it("orders stages by pipeline order and breaks ties by company", () => {
    expect(names(sortApps(list, "stage", "asc"))).toBe("gamma,beta,Alpha");
    expect(names(sortApps(list, "days", "asc"))).toBe("Alpha,gamma,beta");
  });
  it("puts urgent next moves first and closed ones last", () => {
    expect(names(sortApps(list, "next", "asc"))).toBe("gamma,beta,Alpha");
  });
  it("sorts by application date and leaves the input alone", () => {
    expect(names(sortApps(list, "applied", "desc"))).toBe("beta,Alpha,gamma");
    expect(names(list)).toBe("beta,Alpha,gamma");
  });
  it("accepts only known keys", () => {
    expect(isSortKey("fit")).toBe(true);
    expect(isSortKey("__proto__")).toBe(false);
  });
});

const org = (name: string, o: Partial<OrgSortable> = {}): OrgSortable => ({
  name, applications: 1, open: 1, shortlisted: 0, interviewed: 0, offers: 0, rejected: 0,
  first_applied_at: "2026-10-01T00:00:00Z", avg_response_days: null, ...o,
});
const orgNames = (l: OrgSortable[]) => l.map((o) => o.name).join(",");

describe("sortOrgs", () => {
  const list = [
    org("beta", { applications: 3, open: 1, interviewed: 1, rejected: 2, avg_response_days: 4, first_applied_at: "2026-09-01T00:00:00Z" }),
    org("Alpha", { applications: 1, shortlisted: 1, avg_response_days: 2.5 }),
    org("gamma", { applications: 2, open: 2, first_applied_at: null }),
    org("delta", { applications: 3, open: 3, interviewed: 1 }),
  ];
  it("sorts names case-insensitively", () => {
    expect(orgNames(sortOrgs(list, "name", "asc"))).toBe("Alpha,beta,delta,gamma");
  });
  it("sorts by applications, then open ones", () => {
    expect(orgNames(sortOrgs(list, "applications", "desc"))).toBe("delta,beta,gamma,Alpha");
  });
  it("ranks outcomes by how far they got, fewer rejections first on a tie", () => {
    expect(orgNames(sortOrgs(list, "outcomes", "desc"))).toBe("delta,beta,Alpha,gamma");
  });
  it("keeps missing dates and replies last in both directions", () => {
    expect(orgNames(sortOrgs(list, "first", "asc"))).toBe("beta,Alpha,delta,gamma");
    expect(orgNames(sortOrgs(list, "first", "desc"))).toBe("Alpha,delta,beta,gamma");
    expect(orgNames(sortOrgs(list, "response", "asc"))).toBe("Alpha,beta,delta,gamma");
    expect(orgNames(sortOrgs(list, "response", "desc"))).toBe("beta,Alpha,delta,gamma");
  });
  it("accepts only known keys", () => {
    expect(isOrgSortKey("outcomes")).toBe(true);
    expect(isOrgSortKey("constructor")).toBe(false);
  });
});
