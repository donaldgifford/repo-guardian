import type { Context } from "hono";
import { deleteCookie, getCookie, setCookie } from "hono/cookie";
import { EncryptJWT, type JWTPayload, jwtDecrypt } from "jose";

// Session is what the BFF keeps for a signed-in user. It travels only
// inside the encrypted session cookie: there is no server-side store.
export interface Session {
  sub: string;
  name: string;
  accessToken: string;
  // accessTokenExpiresAt is epoch seconds.
  accessTokenExpiresAt: number;
  refreshToken?: string;
  idToken?: string;
  // expiresAt is the absolute session end (epoch seconds). Refreshing the
  // tokens never moves it, so UI_SESSION_TTL caps a session however long
  // the refresh token lives.
  expiresAt: number;
}

// maxChunkBytes keeps each cookie under the ~4 KB per-cookie limit with
// room for the name and attributes.
export const maxChunkBytes = 3800;

// maxChunks bounds how many chunks a read will look for.
const maxChunks = 10;

const header = { alg: "dir", enc: "A256GCM" } as const;

// SealedCookie stores one encrypted value of type T in a __Host- cookie,
// split into numbered chunks when it is too large for one.
export class SealedCookie<T extends object> {
  // name is without the __Host- prefix, which setCookie adds.
  constructor(
    private readonly name: string,
    private readonly keys: Uint8Array[],
  ) {
    if (keys.length === 0) {
      throw new Error("SealedCookie needs at least one key");
    }
  }

  // seal encrypts value with the first key, expiring at expiresAt.
  async seal(value: T, expiresAt: number): Promise<string> {
    const [key] = this.keys;
    return new EncryptJWT({ v: value } as unknown as JWTPayload)
      .setProtectedHeader(header)
      .setIssuedAt()
      .setExpirationTime(expiresAt)
      .encrypt(key as Uint8Array);
  }

  // open decrypts with each key in turn; undefined when no key opens it
  // or it has expired.
  async open(token: string): Promise<T | undefined> {
    for (const key of this.keys) {
      try {
        const { payload } = await jwtDecrypt(token, key, { contentEncryptionAlgorithms: [header.enc], keyManagementAlgorithms: [header.alg] });
        return (payload as { v?: T }).v;
      } catch {
        // Wrong key, tampered, or expired: try the next key.
      }
    }
    return undefined;
  }

  async write(c: Context, value: T, expiresAt: number): Promise<void> {
    const token = await this.seal(value, expiresAt);
    const maxAge = Math.max(0, expiresAt - Math.floor(Date.now() / 1000));
    const had = this.chunkCount(c);

    if (token.length <= maxChunkBytes) {
      setCookie(c, this.name, token, this.attrs(maxAge));
      this.dropChunks(c, 0, had);
      return;
    }

    const parts = Math.ceil(token.length / maxChunkBytes);
    if (parts > maxChunks) {
      throw new Error(`${this.name}: sealed value needs ${parts} chunks, over the limit of ${maxChunks}`);
    }

    for (let i = 0; i < parts; i++) {
      setCookie(c, `${this.name}.${i}`, token.slice(i * maxChunkBytes, (i + 1) * maxChunkBytes), this.attrs(maxAge));
    }
    this.dropChunks(c, parts, had);
    if (getCookie(c, this.name, "host") !== undefined) {
      deleteCookie(c, this.name, this.attrs(0));
    }
  }

  async read(c: Context): Promise<T | undefined> {
    const whole = getCookie(c, this.name, "host");
    if (whole !== undefined) {
      return this.open(whole);
    }

    const n = this.chunkCount(c);
    if (n === 0) {
      return undefined;
    }

    let token = "";
    for (let i = 0; i < n; i++) {
      token += getCookie(c, `${this.name}.${i}`, "host") ?? "";
    }
    return this.open(token);
  }

  clear(c: Context): void {
    if (getCookie(c, this.name, "host") !== undefined) {
      deleteCookie(c, this.name, this.attrs(0));
    }
    this.dropChunks(c, 0, this.chunkCount(c));
  }

  private chunkCount(c: Context): number {
    let n = 0;
    while (n < maxChunks && getCookie(c, `${this.name}.${n}`, "host") !== undefined) {
      n++;
    }
    return n;
  }

  private dropChunks(c: Context, from: number, to: number): void {
    for (let i = from; i < to; i++) {
      deleteCookie(c, `${this.name}.${i}`, this.attrs(0));
    }
  }

  // attrs are the __Host- cookie attributes: HttpOnly, Secure,
  // SameSite=Lax, Path=/ and no Domain.
  private attrs(maxAge: number) {
    return { prefix: "host", httpOnly: true, secure: true, sameSite: "Lax", path: "/", maxAge } as const;
  }
}

// sessionCookie is the signed-in session, __Host-rg_session.
export function sessionCookie(keys: Uint8Array[]): SealedCookie<Session> {
  return new SealedCookie<Session>("rg_session", keys);
}

// newSessionExpiry is the absolute end of a session started now.
export function newSessionExpiry(ttlSeconds: number, now = Date.now()): number {
  return Math.floor(now / 1000) + ttlSeconds;
}
