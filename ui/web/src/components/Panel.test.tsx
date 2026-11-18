import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { Panel } from "./Panel";

describe("Panel", () => {
  test("with a title renders an h3 header, the strip and the body in order, as a section", () => {
    const html = renderToStaticMarkup(
      <Panel title="Compliance" subtitle="over 30d" right={<span>R</span>} strip={<div>strip</div>} aria-label="x">
        body
      </Panel>,
    );
    expect(html).toMatch(/^<section[^>]*data-slot="card"[^>]*aria-label="x"/);
    expect(html).toMatch(/<h3[^>]*>Compliance<\/h3><div[^>]*>over 30d<\/div><div[^>]*data-slot="card-action"[^>]*><span>R<\/span><\/div><\/div><div>strip<\/div><div[^>]*data-slot="card-content"[^>]*>body<\/div><\/section>$/);
  });

  test("a flush body drops its padding", () => {
    const html = renderToStaticMarkup(
      <Panel title="T" bodyClassName="p-0">
        body
      </Panel>,
    );
    expect(html).toMatch(/data-slot="card-content" class="[^"]*\bp-0\b/);
    expect(html).not.toMatch(/data-slot="card-content" class="[^"]*py-2\.5/);
  });

  test("without a title is the bare surface, and the caller's layout wins over the card's column", () => {
    const html = renderToStaticMarkup(<Panel className="flex flex-wrap py-2.5">raw</Panel>);
    expect(html).toMatch(/^<section[^>]*>raw<\/section>$/);
    expect(html).not.toContain("flex-col");
    expect(html).not.toContain("py-0");
    expect(html).toContain("flex-wrap");
  });
});
