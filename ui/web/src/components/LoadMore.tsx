import { Button } from "./ui/button";

// LoadMore is the footer of a paged table: "N loaded[ of M]" and the
// button that asks for the next page while there is one.
export function LoadMore({ loaded, total, hasMore, loading, onMore, noun = "rows" }: { loaded: number; total?: number | undefined; hasMore: boolean; loading: boolean; onMore: () => void; noun?: string }) {
  if (loaded === 0 && !hasMore) return null;
  return (
    <div className="flex items-center justify-between gap-3 border-t border-border px-3.5 py-2 text-xs text-muted-foreground">
      <span>
        {loaded.toLocaleString()} loaded{total !== undefined ? ` of ${total.toLocaleString()}` : ""} {noun}
      </span>
      {hasMore ? (
        <Button size="sm" onClick={onMore} disabled={loading}>
          {loading ? "Loading…" : "Load more"}
        </Button>
      ) : null}
    </div>
  );
}
