// Fails when web/src/api/schema.gen.ts is stale against api/openapi.yaml:
// regenerates into a temp file and compares. The CI `ui` job runs it.
import { $ } from "bun";
import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const committed = "web/src/api/schema.gen.ts";
const out = join(await mkdtemp(join(tmpdir(), "rg-ui-")), "schema.gen.ts");

await $`bunx openapi-typescript ../api/openapi.yaml -o ${out}`.quiet();

const [want, got] = await Promise.all([readFile(out, "utf8"), readFile(committed, "utf8")]);
if (want !== got) {
  console.error(`${committed} is stale: run \`bun run gen:api\` and commit the result`);
  process.exit(1);
}

console.log(`${committed} is current`);
