import { describe, expect, it } from "vitest";
import { isSortKey, sortApps, type Sortable } from "./sort";

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
