import { useQuery } from "@tanstack/react-query";

import { api, unwrap } from "./client";

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
