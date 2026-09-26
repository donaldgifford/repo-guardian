import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";

import { api, type Schemas, unwrap } from "./client";
import type { paths } from "./schema.gen";

// Page is any cursor-paginated list.
interface Page<T> {
  items: T[];
  next_cursor?: string;
}

// maxPages bounds a fetch-everything read (history series), so a huge
// range degrades to a truncated chart instead of an unbounded loop.
export const maxPages = 20;

// fetchAll follows next_cursor until the list ends or maxPages is hit.
export async function fetchAll<T>(page: (cursor: string | undefined) => Promise<Page<T>>): Promise<{ items: T[]; truncated: boolean }> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const p = await page(cursor);
    items.push(...p.items);
    if (!p.next_cursor) {
      return { items, truncated: false };
    }
    cursor = p.next_cursor;
  }
  return { items, truncated: true };
}

export interface UIConfig {
  issuer: string;
  public_url: string;
  session_ttl_seconds: number;
  github_host: string;
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

export function useSummary() {
  return useQuery({ queryKey: ["summary"], queryFn: () => unwrap(api.GET("/summary")) });
}

export function useRules() {
  return useQuery({ queryKey: ["rules"], queryFn: () => unwrap(api.GET("/rules")) });
}

export function useRule(kind: string, name: string) {
  return useQuery({
    queryKey: ["rule", kind, name],
    queryFn: () => unwrap(api.GET("/rules/{kind}/{name}", { params: { path: { kind, name } } })),
  });
}

export function useOrgs() {
  return useQuery({ queryKey: ["orgs"], queryFn: () => unwrap(api.GET("/orgs")) });
}

export function useOrg(org: string | undefined) {
  return useQuery({
    queryKey: ["org", org],
    queryFn: () => unwrap(api.GET("/orgs/{org}", { params: { path: { org: org ?? "" } } })),
    enabled: org !== undefined,
  });
}

// FindingFilters are /findings' query parameters, less the paging ones.
export type FindingFilters = Omit<NonNullable<paths["/findings"]["get"]["parameters"]["query"]>, "cursor" | "limit">;

export function useFindings(filters: FindingFilters) {
  return useInfiniteQuery({
    queryKey: ["findings", filters],
    queryFn: ({ pageParam }) =>
      unwrap(api.GET("/findings", { params: { query: { ...filters, ...(pageParam ? { cursor: pageParam } : {}), limit: 200 } } })),
    initialPageParam: "",
    getNextPageParam: (last: Schemas["FindingPage"]) => last.next_cursor,
    placeholderData: keepPreviousData,
  });
}

export function useRepository(id: number) {
  return useQuery({
    queryKey: ["repository", id],
    queryFn: () => unwrap(api.GET("/repositories/{id}", { params: { path: { id } } })),
  });
}

export function useRepositoryEvents(id: number) {
  return useQuery({
    queryKey: ["repository-events", id],
    queryFn: () => unwrap(api.GET("/repositories/{id}/events", { params: { path: { id }, query: { limit: 50 } } })),
  });
}

export function useRepositoryChecks(id: number) {
  return useQuery({
    queryKey: ["repository-checks", id],
    queryFn: () => unwrap(api.GET("/repositories/{id}/checks", { params: { path: { id }, query: { limit: 20 } } })),
  });
}

export interface HistoryFilters {
  org?: string;
  kind?: string;
  rule?: string;
  from?: string;
}

export function useHistory(filters: HistoryFilters) {
  return useQuery({
    queryKey: ["history", filters],
    queryFn: () =>
      fetchAll((cursor) =>
        unwrap(api.GET("/compliance/history", { params: { query: { ...filters, ...(cursor ? { cursor } : {}), limit: 200 } } })),
      ),
  });
}

export function usePolicy() {
  return useQuery({ queryKey: ["policy"], queryFn: () => unwrap(api.GET("/policy")) });
}

export function useStatus() {
  return useQuery({ queryKey: ["status"], queryFn: () => unwrap(api.GET("/status")), refetchInterval: 30_000 });
}
