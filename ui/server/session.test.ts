import { describe, expect, test } from "bun:test";
import { Hono } from "hono";

import { loadConfig } from "./config";
import { maxChunkBytes, type Session, SealedCookie, newSessionExpiry, sessionCookie } from "./session";
import { validEnv } from "./testenv";

const keysOf = (...secrets: string[]) => loadConfig({ ...validEnv, UI_SESSION_KEYS: secrets.join(",") }).sessionKeys;
const [keyA, keyB] = [`A${"a".repeat(31)}`, `B${"b".repeat(31)}`];

function session(extra: Partial<Session> = {}): Session {
  return {
    sub: "u1",
    name: "Ada",
    accessToken: "access-token-plaintext",
    accessTokenExpiresAt: Math.floor(Date.now() / 1000) + 300,
    refreshToken: "refresh-token-plaintext",
    expiresAt: newSessionExpiry(3600),
    ...extra,
  };
}

// harness mounts write and read routes over one SealedCookie and carries
// cookies between requests like a browser.
function harness(cookie: SealedCookie<Session>) {
  const app = new Hono();
  let value: Session = session();
  app.get("/write", async (c) => {
    await cookie.write(c, value, value.expiresAt);
    return c.text("ok");
  });
  app.get("/read", async (c) => c.json((await cookie.read(c)) ?? null));
  app.get("/clear", (c) => {
    cookie.clear(c);
    return c.text("ok");
  });

  const jar = new Map<string, string>();
  const send = async (path: string) => {
    const res = await app.request(path, { headers: { cookie: [...jar].map(([k, v]) => `${k}=${v}`).join("; ") } });
    for (const sc of res.headers.getSetCookie()) {
      const [pair] = sc.split(";");
      const [k, v] = (pair ?? "").split("=");
      if (!k) continue;
      if (/Max-Age=0/.test(sc)) jar.delete(k);
      else jar.set(k, v ?? "");
    }
    return res;
  };

  return { send, jar, set: (s: Session) => (value = s) };
}

describe("SealedCookie", () => {
  test("round-trips a session through __Host-rg_session", async () => {
    const h = harness(sessionCookie(keysOf(keyA)));
    const res = await h.send("/write");
    const sc = res.headers.getSetCookie()[0] ?? "";
    expect(sc).toStartWith("__Host-rg_session=");
    for (const attr of ["HttpOnly", "Secure", "SameSite=Lax", "Path=/"]) {
      expect(sc).toContain(attr);
    }
    expect(sc).not.toContain("Domain");
    expect(await (await h.send("/read")).json()).toMatchObject({ sub: "u1", accessToken: "access-token-plaintext", refreshToken: "refresh-token-plaintext" });
  });

  test("the cookie value is opaque", async () => {
    const h = harness(sessionCookie(keysOf(keyA)));
    await h.send("/write");
    const token = h.jar.get("__Host-rg_session") ?? "";
    expect(token.split(".")).toHaveLength(5);
    // Markers long enough never to occur by chance in base64url.
    expect(token).not.toContain("plaintext");
  });

  test("splits a large session into chunks and reassembles it", async () => {
    const h = harness(sessionCookie(keysOf(keyA)));
    h.set(session({ idToken: "x".repeat(9000) }));
    await h.send("/write");
    expect(h.jar.has("__Host-rg_session")).toBe(false);
    expect(h.jar.has("__Host-rg_session.0")).toBe(true);
    expect(h.jar.has("__Host-rg_session.2")).toBe(true);
    for (const v of h.jar.values()) {
      expect(v.length).toBeLessThanOrEqual(maxChunkBytes);
    }
    expect(((await (await h.send("/read")).json()) as Session).idToken).toHaveLength(9000);

    // Shrinking back to one cookie removes the stale chunks.
    h.set(session());
    await h.send("/write");
    expect([...h.jar.keys()]).toEqual(["__Host-rg_session"]);
  });

  test("clear removes every chunk", async () => {
    const h = harness(sessionCookie(keysOf(keyA)));
    h.set(session({ idToken: "x".repeat(9000) }));
    await h.send("/write");
    await h.send("/clear");
    expect(h.jar.size).toBe(0);
  });

  test("key rotation: an old key still opens, the new key seals", async () => {
    const old = sessionCookie(keysOf(keyA));
    const rotated = sessionCookie(keysOf(keyB, keyA));
    const token = await old.seal(session(), newSessionExpiry(60));
    expect(await rotated.open(token)).toMatchObject({ sub: "u1" });

    const fresh = await rotated.seal(session(), newSessionExpiry(60));
    expect(await old.open(fresh)).toBeUndefined();
    expect(await sessionCookie(keysOf(keyB)).open(fresh)).toMatchObject({ sub: "u1" });
  });

  test("a dropped key no longer opens its sessions", async () => {
    const token = await sessionCookie(keysOf(keyA)).seal(session(), newSessionExpiry(60));
    expect(await sessionCookie(keysOf(keyB)).open(token)).toBeUndefined();
  });

  test("an expired session does not open", async () => {
    const c = sessionCookie(keysOf(keyA));
    const token = await c.seal(session(), Math.floor(Date.now() / 1000) - 1);
    expect(await c.open(token)).toBeUndefined();
  });

  test("a tampered token does not open", async () => {
    const c = sessionCookie(keysOf(keyA));
    const token = await c.seal(session(), newSessionExpiry(60));
    const parts = token.split(".");
    parts[3] = `${parts[3]?.slice(0, -2)}AA`;
    expect(await c.open(parts.join("."))).toBeUndefined();
  });
});
