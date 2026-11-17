import { describe, expect, it } from "vitest";
import { GET } from "./route";

describe("healthz", () => {
  it("answers without calling other services", async () => {
    const res = GET();
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("ok");
  });
});
