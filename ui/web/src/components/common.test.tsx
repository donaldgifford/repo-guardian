import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { Evidence, ExternalLink } from "./common";

describe("Evidence", () => {
  test("renders HTML in repository-controlled evidence as text", () => {
    const html = renderToStaticMarkup(
      <Evidence
        finding={{
          reason: "assertion_failed",
          evidence: { reason: "assertion_failed", path: "<b>renovate.json</b>", message: `<img src=x onerror="alert(1)">` },
        }}
      />,
    );
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<b>");
    expect(html).toContain("&lt;img src=x onerror=&quot;alert(1)&quot;&gt;");
  });

  test("migrated_from_v1 says the details come on the next check", () => {
    const html = renderToStaticMarkup(
      <Evidence finding={{ reason: "migrated_from_v1", evidence: { reason: "migrated_from_v1", v1_actionable_since: "2026-01-01T00:00:00Z" } }} />,
    );
    expect(html).toContain("last checked by v1; details on next check");
  });

  test("an unknown reason shows only its code", () => {
    const html = renderToStaticMarkup(<Evidence finding={{ reason: "brand_new_reason" }} />);
    expect(html).toBe('<div class="space-y-0.5"><div><span class="font-mono text-xs text-muted-foreground">brand_new_reason</span></div></div>');
  });
});

test("external links never hand over the opener or referrer", () => {
  const html = renderToStaticMarkup(<ExternalLink href="https://github.com/acme/web/pull/1">PR #1</ExternalLink>);
  expect(html).toContain('rel="noopener noreferrer"');
  expect(html).toContain('target="_blank"');
});
