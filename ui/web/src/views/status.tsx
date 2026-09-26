import { useStatus } from "../api/queries";
import { Loading } from "../components/common";
import { Badge } from "../components/ui/badge";
import { Card, CardContent } from "../components/ui/card";
import { formatPercent, humanize, relativeTime } from "../lib/format";

// stateTone maps a component state onto a badge tone.
export function stateTone(state: string): "compliant" | "unknown" | "non_compliant" | "neutral" {
  switch (state) {
    case "operational":
      return "compliant";
    case "degraded":
      return "unknown";
    case "down":
      return "non_compliant";
    default:
      return "neutral";
  }
}

// StatusView is the public status page: no login, no names.
export function StatusView() {
  const status = useStatus();
  return (
    <main className="mx-auto max-w-2xl space-y-6 px-4 py-10">
      <h1 className="text-xl font-semibold">repo-guardian status</h1>
      {!status.data ? (
        <Loading />
      ) : (
        <>
          <Card>
            <CardContent className="flex items-center justify-between p-4">
              <span className="text-lg">{humanize(status.data.state)}</span>
              <span className="text-sm text-muted-foreground">updated {relativeTime(status.data.updated_at)}</span>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="divide-y divide-border p-0">
              {status.data.components.map((c) => (
                <div key={c.name} className="flex items-center justify-between gap-4 px-4 py-3 text-sm">
                  <span className="font-medium">{humanize(c.name)}</span>
                  <span className="text-muted-foreground">{c.detail}</span>
                  <Badge tone={stateTone(c.state)}>{c.state}</Badge>
                </div>
              ))}
            </CardContent>
          </Card>
          <p className="text-sm">
            Fleet compliance: <span className="font-medium">{status.data.compliance.measured ? formatPercent(status.data.compliance.percent) : "unmeasured"}</span>
          </p>
        </>
      )}
    </main>
  );
}
