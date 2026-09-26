// patchSearch returns prev with patch applied, dropping a key whose new
// value is undefined or "" rather than setting it to undefined: search
// params are optional keys, and an empty one should leave the URL.
export function patchSearch<T extends object>(prev: T, patch: Record<string, string | boolean | undefined>): T {
  const next = { ...prev } as Record<string, unknown>;
  for (const [k, v] of Object.entries(patch)) {
    if (v === undefined || v === "") {
      delete next[k];
    } else {
      next[k] = v;
    }
  }
  return next as T;
}
