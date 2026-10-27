// The Playwright stack (IMPL-0025 22.4), run by Bun from
// playwright.config.ts's webServer: a Postgres container migrated with
// `repo-guardian migrate` (after e2e/roles.sql) and seeded, the mock issuer, the Go `api`
// role verifying the issuer's JWT access tokens, and the BFF serving
// the built SPA on http://localhost:3100. A second database in the
// same container, seeded without the Remediation App
// (e2e/seed-no-remediate.sql), gets its own api role and BFF on
// http://localhost:3101 for the `no-remediation` project (IMPL-0032
// 8.7). Ctrl-C (or Playwright's SIGTERM) tears it all down.
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { $ } from "bun";

import { createApp } from "../server/app";
import { loadConfig } from "../server/config";
import { startMockIssuer } from "../server/testing/mock-issuer";

const repoRoot = fileURLToPath(new URL("../../", import.meta.url));
const uiRoot = fileURLToPath(new URL("../", import.meta.url));
const bin = process.env.E2E_BINARY ?? join(repoRoot, "build/bin/repo-guardian");
export const uiPort = Number(process.env.E2E_UI_PORT ?? 3100);
// The "not enabled" stack sits one port up from each of the main one's.
export const noRemediationPort = uiPort + 1;
const apiAddr = "127.0.0.1:18080";
const noRemediationApiAddr = "127.0.0.1:18081";
const noRemediationDb = "repoguardian_norem";
const container = `rg-e2e-${process.pid}`;
export const containerLabel = "repo-guardian-e2e";

const cleanups: (() => void | Promise<void>)[] = [];
async function teardown(code: number) {
  for (const c of cleanups.reverse()) {
    try {
      await c();
    } catch {
      // best effort: the rest must still run
    }
  }
  process.exit(code);
}
process.on("SIGINT", () => void teardown(0));
process.on("SIGTERM", () => void teardown(0));

async function waitFor(what: string, probe: () => Promise<boolean>, timeoutMs = 60_000) {
  const until = Date.now() + timeoutMs;
  while (Date.now() < until) {
    if (await probe().catch(() => false)) return;
    await Bun.sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
}

try {
  if (!process.env.E2E_BINARY) {
    await $`go build -o ${bin} ./cmd/repo-guardian`.cwd(repoRoot);
  }
  await $`bun run build`.cwd(uiRoot).quiet();

  // Containers carry a label so a stack killed before its own teardown
  // (Playwright may not wait for it) is swept here and by teardown.ts.
  await $`docker ps -aq --filter label=${containerLabel}`.quiet().then(async (r) => {
    const ids = r.text().trim().split("\n").filter(Boolean);
    if (ids.length > 0) await $`docker rm -f ${ids}`.quiet();
  });
  await $`docker run -d --rm --label ${containerLabel} --name ${container} -e POSTGRES_USER=repoguardian -e POSTGRES_PASSWORD=e2e -e POSTGRES_DB=repoguardian -p 127.0.0.1::5432 postgres:18.4`.quiet();
  cleanups.push(() => $`docker rm -f ${container}`.quiet().then(() => undefined));
  const hostPort = (await $`docker port ${container} 5432/tcp`.text()).trim().split("\n")[0] ?? "";
  const dsn = `postgres://repoguardian:e2e@${hostPort}/repoguardian?sslmode=disable`;
  // Over TCP, not the socket: the image's first-boot init runs a
  // socket-only server that answers pg_isready and then restarts.
  await waitFor(
    "postgres",
    async () => (await $`docker exec ${container} pg_isready -h 127.0.0.1 -U repoguardian -d repoguardian`.nothrow().quiet()).exitCode === 0,
  );

  const psql = (db: string, file: string) => $`docker exec -i ${container} psql -q -U repoguardian -d ${db} -v ON_ERROR_STOP=1 < ${join(uiRoot, file)}`.quiet();
  await psql("repoguardian", "e2e/roles.sql");
  await $`${bin} migrate --dsn ${dsn} --policy=`.quiet();
  await psql("repoguardian", "e2e/seed.sql");

  // The second database: the roles are cluster-wide, the schema grant
  // is per database, the seed is the base one minus the Remediation App.
  await $`docker exec ${container} psql -q -U repoguardian -d repoguardian -v ON_ERROR_STOP=1 -c ${`CREATE DATABASE ${noRemediationDb}`}`.quiet();
  await $`docker exec ${container} psql -q -U repoguardian -d ${noRemediationDb} -v ON_ERROR_STOP=1 -c ${"GRANT USAGE ON SCHEMA public TO rg_evaluator, rg_remediator"}`.quiet();
  const noRemediationDsn = dsn.replace("/repoguardian?", `/${noRemediationDb}?`);
  await $`${bin} migrate --dsn ${noRemediationDsn} --policy=`.quiet();
  await psql(noRemediationDb, "e2e/seed.sql");
  await psql(noRemediationDb, "e2e/seed-no-remediate.sql");

  // One user in one group that sees acme only: globex must never show.
  const issuer = await startMockIssuer({ apiAudience: "repo-guardian-api", groups: ["acme-devs"], accessTokenTtl: 600 });
  cleanups.push(() => issuer.stop());

  const authz = join(mkdtempSync(join(tmpdir(), "rg-e2e-")), "authz.yaml");
  writeFileSync(authz, 'groups:\n  "acme-devs": ["acme"]\ndefaultOrgs: []\nclients: {}\n');

  const issuerUrl = issuer.url.origin;
  const startApi = async (addr: string, metrics: string, storeDsn: string) => {
    const api = Bun.spawn([bin, "api"], {
      env: {
        ...process.env,
        API_LISTEN_ADDR: addr,
        METRICS_ADDR: metrics,
        LOG_LEVEL: "warn",
        STORE_RO_DSN: storeDsn,
        OIDC_ISSUER: issuerUrl,
        OIDC_AUDIENCE: "repo-guardian-api",
        API_AUTHZ_CONFIG: authz,
      },
      stdout: "inherit",
      stderr: "inherit",
    });
    cleanups.push(() => api.kill());
    await waitFor(`api on ${addr}`, async () => (await fetch(`http://${addr}/readyz`)).ok);
  };
  // tokenAuthMethod differs per BFF so every e2e run signs in once with
  // each client authentication method (IMPL-0033 2.5): the main stack on
  // client_secret_post, the no-remediation stack on client_secret_basic.
  const startUI = (port: number, upstream: string, tokenAuthMethod: "client_secret_post" | "client_secret_basic") => {
    const config = loadConfig({
      OIDC_ISSUER: issuerUrl,
      OIDC_CLIENT_ID: issuer.clientId,
      OIDC_CLIENT_SECRET: issuer.clientSecret,
      API_UPSTREAM: `http://${upstream}`,
      PUBLIC_URL: `http://localhost:${port}`,
      UI_SESSION_KEYS: "e2e-session-key-e2e-session-key-e2e!",
      PORT: String(port),
      OIDC_TOKEN_AUTH_METHOD: tokenAuthMethod,
    });
    const server = Bun.serve({ port, hostname: "localhost", fetch: createApp({ config, staticDir: join(uiRoot, "dist") }).fetch });
    cleanups.push(() => server.stop(true));
  };

  await startApi(apiAddr, "127.0.0.1:19090", dsn);
  startUI(uiPort, apiAddr, "client_secret_post");
  await startApi(noRemediationApiAddr, "127.0.0.1:19091", noRemediationDsn);
  startUI(noRemediationPort, noRemediationApiAddr, "client_secret_basic");

  console.log(`e2e stack ready on http://localhost:${uiPort} (no-remediation on http://localhost:${noRemediationPort})`);
} catch (err) {
  console.error(err);
  await teardown(1);
}
