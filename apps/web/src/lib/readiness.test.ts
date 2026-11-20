import { describe, expect, it } from "vitest";
import { readiness } from "./readiness";

describe("readiness", () => {
  it("weighs quiz, apply momentum and interviews equally", () => {
    const r = readiness({
      accuracy7d: 80,
      followThrough7d: null,
      quizDays: [{ day: "2026-11-20", answered: 7 }, { day: "2026-11-19", answered: 3 }, { day: "2026-11-18", answered: 0 }],
      applied7d: 5,
      activeInterviews: 2,
      today: "2026-11-20",
    });
    // quiz: (80 + 2/7·100) / 2 = 54; momentum 5/10 = 50; interviews 2/3 = 67
    expect(r).toMatchObject({ quiz: 54, momentum: 50, interviews: 67, index: 57 });
    expect(r.detail.quizDays7d).toBe(2);
  });

  it("counts only the last 7 days of quizzes and caps each part at 100", () => {
    const days = Array.from({ length: 10 }, (_, i) => ({ day: `2026-11-${String(20 - i).padStart(2, "0")}`, answered: 1 }));
    const r = readiness({ accuracy7d: 100, followThrough7d: 100, quizDays: days, applied7d: 25, activeInterviews: 6, today: "2026-11-20" });
    expect(r).toMatchObject({ quiz: 100, momentum: 100, interviews: 100, index: 100 });
  });

  it("counts how well improvement drills were followed once one has been checked", () => {
    const base = { accuracy7d: 80, quizDays: [{ day: "2026-11-20", answered: 7 }, { day: "2026-11-19", answered: 3 }], applied7d: 0, activeInterviews: 0, today: "2026-11-20" };
    // 0.4·80 + 0.3·(2/7·100) + 0.3·follow-through
    expect(readiness({ ...base, followThrough7d: 100 }).quiz).toBe(71);
    expect(readiness({ ...base, followThrough7d: 0 }).quiz).toBe(41);
  });

  it("is zero with nothing done", () => {
    expect(readiness({ accuracy7d: null, followThrough7d: null, quizDays: [], applied7d: 0, activeInterviews: 0, today: "2026-11-20" }).index).toBe(0);
  });
});
