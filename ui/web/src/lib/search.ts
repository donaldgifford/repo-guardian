// patchSearch returns prev with patch applied. A key whose new value is
// undefined or "" is set to undefined, not deleted: the router drops
// undefined values from the URL, but `retainSearchParams` re-adds a key
// that is *absent* from the next search, so deleting a retained key
// (org, range) would make it impossible to clear.
export function patchSearch<T extends object>(prev: T, patch: Record<string, string | boolean | undefined>): T {
  const next = { ...prev } as Record<string, unknown>;
  for (const [k, v] of Object.entries(patch)) {
    next[k] = v === undefined || v === "" ? undefined : v;
  }
  return next as T;
}
