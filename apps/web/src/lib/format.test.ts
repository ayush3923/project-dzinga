import { describe, expect, it } from "vitest";
import { brief, companyKey, daysBetween, initials, isStage, parsePipeline, providerOf, refreshStamp, relativeDays, roundsLabel } from "./format";

const zero = { applied: 0, assessment: 0, interview: 0, offer: 0, rejected: 0 };

describe("refreshStamp", () => {
  it("formats as DD MM YY (HH:MM) in 24-hour time", () => {
    expect(refreshStamp("2026-11-17T05:39:00Z", "Asia/Kolkata")).toBe("17 11 26 (11:09)");
    expect(refreshStamp("2026-01-02T18:05:00Z", "Asia/Kolkata")).toBe("02 01 26 (23:35)");
  });
  it("says never without a valid time", () => {
    expect(refreshStamp(null)).toBe("never");
    expect(refreshStamp("not a date")).toBe("never");
  });
});

describe("roundsLabel", () => {
  it("shows rounds done and what is next", () => {
    expect(roundsLabel(1, "Round 2 interview", "interview")).toBe("1 round done · Next: Round 2 interview");
    expect(roundsLabel(2, "", "interview")).toBe("2 rounds done");
    expect(roundsLabel(0, "HR call", "applied")).toBe("Next: HR call");
  });
  it("is empty with nothing recorded, and drops the next step once rejected", () => {
    expect(roundsLabel(0, "", "interview")).toBe("");
    expect(roundsLabel(3, "Round 4", "rejected")).toBe("3 rounds done");
  });
});

describe("brief", () => {
  it("says nothing needs attention when the pipeline is empty", () => {
    expect(brief(zero, 0)).toBe("Nothing needs you right now.");
  });
  it("mentions in-flight applications when calm", () => {
    expect(brief({ ...zero, applied: 3 }, 0)).toBe("3 applications are in flight. Nothing needs you right now.");
  });
  it("lists what needs attention, offers first, with correct plurals", () => {
    expect(brief({ ...zero, offer: 1, interview: 2, applied: 5 }, 1)).toBe(
      "You have 1 offer to decide on, 2 processes in interviews and 1 application that has gone quiet.",
    );
  });
});

describe("dates", () => {
  const now = new Date("2026-10-14T12:00:00Z");
  it("counts whole days and never goes negative", () => {
    expect(daysBetween("2026-10-04T12:00:00Z", now)).toBe(10);
    expect(daysBetween("2026-10-20T12:00:00Z", now)).toBe(0);
  });
  it("formats relative days", () => {
    expect(relativeDays("2026-10-14T08:00:00Z", now)).toBe("today");
    expect(relativeDays("2026-10-13T08:00:00Z", now)).toBe("yesterday");
    expect(relativeDays("2026-09-30T12:00:00Z", now)).toBe("2 wk ago");
  });
});

describe("misc", () => {
  it("makes initials", () => {
    expect(initials("Kestrel AI")).toBe("KA");
    expect(initials("Northwind")).toBe("NO");
    expect(initials("")).toBe("?");
  });
  it("validates stages", () => {
    expect(isStage("offer")).toBe(true);
    expect(isStage("hired")).toBe(false);
    expect(isStage(undefined)).toBe(false);
  });
});

describe("providerOf", () => {
  it("recognises the user's providers", () => {
    expect(providerOf("someone@yahoo.co.in").id).toBe("yahoo");
    expect(providerOf("someone@outlook.com").id).toBe("outlook");
    expect(providerOf("someone@live.in").id).toBe("outlook");
    expect(providerOf("x@gmail.com").id).toBe("gmail");
    expect(providerOf("x@icloud.com").id).toBe("icloud");
    expect(providerOf("demo@example.com").id).toBe("other");
    expect(providerOf("not-an-address").id).toBe("other");
  });
});

describe("parsePipeline", () => {
  it("names each stage's model", () => {
    expect(parsePipeline("retrieval=claude-sonnet-4-6;summary=claude-sonnet-5;scoring=claude-opus-5-5")).toEqual([
      { task: "Retrieval", model: "Sonnet 4.6" },
      { task: "Summary", model: "Sonnet 5" },
      { task: "Scoring", model: "Opus 5.5" },
    ]);
  });
  it("tolerates empty or malformed input", () => {
    expect(parsePipeline("")).toEqual([]);
    expect(parsePipeline("junk;x=")).toEqual([]);
  });
});

describe("companyKey", () => {
  it("keeps only letters and digits, lower-cased, like the services' OrgKey", () => {
    expect(companyKey("HCL Software")).toBe(companyKey("HCLSoftware"));
    expect(companyKey("PhonePe Ltd.")).toBe("phonepeltd");
    expect(companyKey("Société Générale")).toBe("sociétégénérale");
    expect(companyKey("!!!")).toBe("");
  });
});
