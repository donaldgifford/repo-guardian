import type { Schemas } from "../api/client";
import { Card, CardContent, CardHeader } from "../components/ui/card";

// AccessView is shown to a signed-in caller who may see no org: /me
// says who they are, so they can quote it when asking for access.
export function AccessView({ me }: { me: Schemas["Me"] }) {
  return (
    <Card className="mx-auto mt-16 max-w-lg">
      <CardHeader>
        <h1 className="text-lg font-semibold">You don't have access to any organization yet</h1>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <p>
          You are signed in, but none of your groups is mapped to an organization in repo-guardian. Ask an administrator to add one of your
          groups to the API's authorization config, quoting the identity below.
        </p>
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
          <dt className="text-muted-foreground">Name</dt>
          <dd>{me.name || "—"}</dd>
          <dt className="text-muted-foreground">Subject</dt>
          <dd className="font-mono break-all">{me.subject}</dd>
        </dl>
        <form method="post" action="/auth/logout">
          <button type="submit" className="underline underline-offset-2">
            Sign out
          </button>
        </form>
      </CardContent>
    </Card>
  );
}
