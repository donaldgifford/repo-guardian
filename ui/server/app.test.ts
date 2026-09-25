import { describe, expect, test } from "bun:test";

import { createApp } from "./app";

describe("app", () => {
  test("serves /healthz", async () => {
    const res = await createApp().request("/healthz");
    expect(res.status).toBe(200);
    expect(await res.text()).toBe("ok");
  });
});
