// The changelog model shared by gen-changelog.ts and check-changelog.ts
// (DESIGN-0037 §Status page changelog, IMPL-0032 Phase 7): CHANGELOG.yaml
// parsed, validated against the chart's appVersion, and shaped for the
// bundle.

export const KIND_TAGS = ["new", "improved", "fixed", "breaking"] as const;
export const AREA_TAGS = ["fleet", "findings", "policy", "ui", "api", "chart"] as const;
export const TAGS: readonly string[] = [...KIND_TAGS, ...AREA_TAGS];

export interface ChangelogEntry {
  version: string;
  date: string;
  title: string;
  summary: string;
  tags: string[];
}

// Bundle is what web/src/generated/changelog.json holds: the entries
// newest first and `running`, the newest version, which the gate makes
// the chart appVersion — the release this bundle ships in.
export interface Bundle {
  running: string;
  entries: ChangelogEntry[];
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

// parseChangelog reads the YAML into entries, failing on a shape it does
// not know rather than guessing at one.
export function parseChangelog(text: string): ChangelogEntry[] {
  const doc: unknown = Bun.YAML.parse(text);
  if (!Array.isArray(doc)) throw new Error("CHANGELOG.yaml must be a list of entries");
  return doc.map((raw, i) => {
    if (!isRecord(raw)) throw new Error(`entry ${i}: not a mapping`);
    const str = (k: string): string => {
      const v = raw[k];
      if (typeof v !== "string" || v.trim() === "") throw new Error(`entry ${i}: ${k} must be a non-empty string`);
      return v;
    };
    const tags = raw.tags;
    if (!Array.isArray(tags) || tags.length === 0 || !tags.every((t) => typeof t === "string")) throw new Error(`entry ${i}: tags must be a non-empty list of strings`);
    // YAML reads an unquoted 2026-10-23 as a date; keep the day as written.
    const date = raw.date instanceof Date ? raw.date.toISOString().slice(0, 10) : str("date");
    return { version: str("version"), date, title: str("title"), summary: str("summary"), tags: tags as string[] };
  });
}

// validateChangelog is the gate's rules; each failure is one line.
export function validateChangelog(entries: readonly ChangelogEntry[], appVersion: string): string[] {
  const errors: string[] = [];
  const newest = entries[0];
  if (!newest) return ["the changelog has no entries"];
  if (newest.version !== appVersion) errors.push(`the newest entry is ${newest.version} but charts/repo-guardian/Chart.yaml appVersion is ${appVersion}: add an entry with the chart bump`);
  const seen = new Set<string>();
  let prevDate: string | undefined;
  entries.forEach((e, i) => {
    const where = `entry ${i} (${e.version})`;
    if (seen.has(e.version)) errors.push(`${where}: version repeats`);
    seen.add(e.version);
    if (!/^\d{4}-\d{2}-\d{2}$/.test(e.date) || Number.isNaN(Date.parse(e.date))) errors.push(`${where}: date ${JSON.stringify(e.date)} is not YYYY-MM-DD`);
    else if (prevDate !== undefined && e.date > prevDate) errors.push(`${where}: date ${e.date} is after the newer entry's ${prevDate}; dates must descend`);
    if (/^\d{4}-\d{2}-\d{2}$/.test(e.date)) prevDate = e.date;
    for (const t of e.tags) if (!TAGS.includes(t)) errors.push(`${where}: tag ${JSON.stringify(t)} is not one of ${TAGS.join(", ")}`);
    if (!e.tags.some((t) => (KIND_TAGS as readonly string[]).includes(t))) errors.push(`${where}: needs one of ${KIND_TAGS.join(", ")}`);
  });
  return errors;
}

export function toBundle(entries: readonly ChangelogEntry[]): Bundle {
  return { running: entries[0]?.version ?? "", entries: [...entries] };
}

// chartAppVersion reads appVersion from the chart without a YAML
// dependency on the chart's shape beyond that one line.
export function chartAppVersion(chartYaml: string): string {
  const m = /^appVersion:\s*"?([^"\s]+)"?\s*$/m.exec(chartYaml);
  if (!m?.[1]) throw new Error("charts/repo-guardian/Chart.yaml has no appVersion");
  return m[1];
}

export const changelogPath = "CHANGELOG.yaml";
export const chartPath = "../charts/repo-guardian/Chart.yaml";
export const bundlePath = "web/src/generated/changelog.json";

export function renderBundle(bundle: Bundle): string {
  return JSON.stringify(bundle, null, 2) + "\n";
}
