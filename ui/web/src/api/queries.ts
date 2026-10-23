import { type InfiniteData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ProblemError, type Schemas, toProblem, unwrap } from "./client";

export interface UIConfig {
  issuer: string;
  public_url: string;
  session_ttl_seconds: number;
  github_host: string;
  // docs_url is set when the operator configured UI_DOCS_URL; it adds
  // the Docs group and the avatar menu entry.
  docs_url?: string;
}

export function useUIConfig() {
  return useQuery({
    queryKey: ["ui-config"],
    queryFn: async (): Promise<UIConfig> => (await fetch("/ui/config")).json() as Promise<UIConfig>,
    staleTime: Infinity,
  });
}

export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: () => unwrap(api.GET("/me")), staleTime: 5 * 60_000 });
}

export function useStatus() {
  return useQuery({ queryKey: ["status"], queryFn: () => unwrap(api.GET("/status")), refetchInterval: 30_000 });
}

// remediationEnabled reads Status.apps: the Remediation App is
// installed when it is listed. Every gated panel decides from this and
// nothing else (DESIGN-0037 §Gated panels).
export function remediationEnabled(status: Pick<Schemas["Status"], "apps"> | undefined): boolean | undefined {
  return status === undefined ? undefined : status.apps.some((a) => a.app === "remediate");
}

// useRemediationEnabled is undefined until /status has answered.
export function useRemediationEnabled(): boolean | undefined {
  return remediationEnabled(useStatus().data);
}

export function useSummary() {
  return useQuery({ queryKey: ["summary"], queryFn: () => unwrap(api.GET("/summary")) });
}

export function useControls(org?: string) {
  return useQuery({ queryKey: ["controls", org ?? ""], queryFn: () => unwrap(api.GET("/controls", { params: { query: org ? { org } : {} } })) });
}

export function useControl(id: string) {
  return useQuery({ queryKey: ["control", id], queryFn: () => unwrap(api.GET("/controls/{id}", { params: { path: { id } } })) });
}

export function useOrgs() {
  return useQuery({ queryKey: ["orgs"], queryFn: () => unwrap(api.GET("/orgs")) });
}

export function useOrg(org: string, enabled = true) {
  return useQuery({ queryKey: ["org", org], queryFn: () => unwrap(api.GET("/orgs/{org}", { params: { path: { org } } })), enabled });
}

export interface RepositoryFilter {
  org?: string;
  active?: boolean;
  q?: string;
  policy_state?: string;
}

export const repositoryPageSize = 50;

// useRepositories pages /repositories by its keyset cursor: the list,
// the orgs tree, the Parked tab and the palette all read it, each with
// its own filter. `enabled` lets a caller hold the request until it has
// something to ask (a collapsed group, an empty palette query).
export function useRepositories(f: RepositoryFilter, enabled = true) {
  return useInfiniteQuery({
    queryKey: ["repositories", f],
    queryFn: ({ pageParam }) => unwrap(api.GET("/repositories", { params: { query: { ...f, limit: repositoryPageSize, ...(pageParam ? { cursor: pageParam } : {}) } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
    enabled,
  });
}

// pageItems flattens an infinite query's pages; undefined until the
// first page lands, so callers keep their loading state.
export function pageItems<T>(data: InfiniteData<{ items: T[] }, unknown> | undefined): T[] | undefined {
  return data?.pages.flatMap((p) => p.items);
}

export function useRepository(id: number) {
  return useQuery({ queryKey: ["repository", id], queryFn: () => unwrap(api.GET("/repositories/{id}", { params: { path: { id } } })) });
}

export function useRepositoryControls(id: number) {
  return useQuery({
    queryKey: ["repository-controls", id],
    queryFn: () => unwrap(api.GET("/repositories/{id}/controls", { params: { path: { id } } })),
  });
}

export function useRepositoryEvaluations(id: number) {
  return useQuery({
    queryKey: ["repository-evaluations", id],
    queryFn: () => unwrap(api.GET("/repositories/{id}/evaluations", { params: { path: { id }, query: { limit: 20 } } })),
  });
}

export function useRepositoryEvents(id: number) {
  return useQuery({
    queryKey: ["repository-events", id],
    queryFn: () => unwrap(api.GET("/repositories/{id}/events", { params: { path: { id }, query: { limit: 50 } } })),
  });
}

export interface ResultsFilter {
  bucket?: string;
  control?: string;
  rule?: string;
  org?: string;
  decision?: string;
  pr_stale?: boolean;
  sort?: "since_asc" | "since_desc" | "pr_age";
}

export const resultsPageSize = 50;

// useResults pages /results by its keyset cursor; `total` rides on the
// first page only, so callers read it from pages[0].
export function useResults(f: ResultsFilter, enabled = true) {
  return useInfiniteQuery({
    queryKey: ["results", f],
    queryFn: ({ pageParam }) => unwrap(api.GET("/results", { params: { query: { ...f, limit: resultsPageSize, ...(pageParam ? { cursor: pageParam } : {}) } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
    enabled,
  });
}

// exportCap bounds a CSV export of /results (DESIGN-0037 OQ6): the
// export pages up to this many rows and says when it stopped short.
export const exportCap = 10_000;

// fetchAllResults walks /results for an export, stopping at the cap.
// fetchPages walks a keyset-paged list to its end or to `cap` rows,
// 200 a page; `truncated` says the cap cut it short.
async function fetchPages<T>(page: (query: { limit: number; cursor?: string }) => Promise<{ items: T[]; next_cursor?: string }>, cap: number): Promise<{ rows: T[]; truncated: boolean }> {
  const rows: T[] = [];
  let cursor: string | undefined;
  for (;;) {
    const p = await page({ limit: Math.min(200, cap - rows.length), ...(cursor ? { cursor } : {}) });
    rows.push(...p.items);
    cursor = p.next_cursor;
    if (!cursor) return { rows, truncated: false };
    if (rows.length >= cap) return { rows: rows.slice(0, cap), truncated: true };
  }
}

// fetchAllResults pages /results for an export, up to `cap` rows.
export function fetchAllResults(f: ResultsFilter, cap = exportCap): Promise<{ rows: Schemas["ResultRow"][]; truncated: boolean }> {
  return fetchPages((q) => unwrap(api.GET("/results", { params: { query: { ...f, ...q } } })), cap);
}

export interface RemediationFilter {
  kind?: string;
  state?: Schemas["RemediationState"];
  org?: string;
  hold?: Schemas["Hold"][];
}

// openPullRequestsCap bounds the open-PR listings the orgs tree and PR
// activity count from; past it the counts are of the first 1,000.
export const openPullRequestsCap = 1_000;

export function fetchAllRemediations(f: RemediationFilter, cap: number): Promise<{ rows: Schemas["Remediation"][]; truncated: boolean }> {
  return fetchPages((q) => unwrap(api.GET("/remediations", { params: { query: { ...f, ...q } } })), cap);
}

// useOpenPullRequests lists the open pull-request remediations in an
// org (or every visible org), for counting per repository or per org.
// Callers gate it on the Remediation App: without it there is nothing
// to list.
export function useOpenPullRequests(org: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ["open-prs", org ?? ""],
    queryFn: () => fetchAllRemediations({ kind: "pull_request", state: "open", ...(org ? { org } : {}) }, openPullRequestsCap),
    enabled,
  });
}

export interface HistoryFilter {
  days: number;
  control?: string;
  org?: string;
  aggregate?: "none" | "fleet" | "org" | "control";
}

export function useComplianceHistory(f: HistoryFilter) {
  return useQuery({ queryKey: ["history", f], queryFn: () => unwrap(api.GET("/compliance/history", { params: { query: f } })) });
}

export function usePolicies() {
  return useQuery({ queryKey: ["policies"], queryFn: () => unwrap(api.GET("/policies")) });
}

// evaluateUnsupportedKey records that this API answered 501 to the
// re-evaluate POST (no Temporal client): the button stays hidden.
export const evaluateUnsupportedKey = ["evaluate-unsupported"] as const;

export function useEvaluateUnsupported(): boolean {
  const q = useQuery({ queryKey: evaluateUnsupportedKey, queryFn: () => false, staleTime: Infinity, initialData: false });
  return q.data;
}

// useEvaluate POSTs a re-evaluation. A 202 is success; 501 hides the
// button for the session; anything else surfaces as a problem.
export function useEvaluate(id: number) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const { error, response } = await api.POST("/repositories/{id}/evaluate", { params: { path: { id } } });
      if (response.status === 501) {
        client.setQueryData(evaluateUnsupportedKey, true);
        return "unsupported" as const;
      }
      if (!response.ok) {
        throw new ProblemError(toProblem(error, response.status, response.statusText));
      }
      return "accepted" as const;
    },
  });
}
