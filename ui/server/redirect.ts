// safeReturnPath accepts only a same-origin relative path to send the
// user back to after login; anything else (absolute URLs,
// protocol-relative "//host", backslash tricks, other origins) becomes
// "/". This is the open-redirect guard.
export function safeReturnPath(raw: string | undefined, origin: URL): string {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.includes("\\")) {
    return "/";
  }

  let u: URL;
  try {
    u = new URL(raw, origin);
  } catch {
    return "/";
  }

  if (u.origin !== origin.origin || u.pathname.startsWith("/auth/")) {
    return "/";
  }

  return `${u.pathname}${u.search}${u.hash}`;
}
