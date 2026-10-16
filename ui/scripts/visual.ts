// visual runs e2e/visual.spec.ts against the Chromium inside the pinned
// mcr.microsoft.com/playwright image (IMPL-0031 6.2), so the committed
// screenshots render the same on a laptop and in CI. The image gets no
// code: it runs `playwright run-server` from the host's node_modules
// (same version as the test runner by construction), and the runner
// connects to it with PW_TEST_CONNECT_WS_ENDPOINT. The e2e stack still
// starts on the host; PW_TEST_CONNECT_EXPOSE_NETWORK=<loopback> routes
// the remote browser's localhost requests back through the runner, so
// the __Host- session cookie keeps its secure localhost origin.
//
//   bun scripts/visual.ts                    compare against e2e/__screenshots__
//   bun scripts/visual.ts --update-snapshots regenerate them
import { fileURLToPath } from "node:url";

import { $ } from "bun";

import { version } from "../node_modules/@playwright/test/package.json";

const image = process.env.PLAYWRIGHT_IMAGE ?? `mcr.microsoft.com/playwright:v${version}-noble`;
const port = Number(process.env.UI_VISUAL_PORT ?? 3300);
const container = "repo-guardian-visual-browser";
const uiRoot = fileURLToPath(new URL("../", import.meta.url));

async function listening(): Promise<boolean> {
  try {
    const socket = await Bun.connect({ hostname: "127.0.0.1", port, socket: { data() {} } });
    socket.end();
    return true;
  } catch {
    return false;
  }
}

await $`docker rm -f ${container}`.quiet().nothrow();
await $`docker run -d --rm --name ${container} -p 127.0.0.1:${port}:${port} -v ${uiRoot}node_modules:/work/node_modules:ro -w /work ${image} node node_modules/playwright/cli.js run-server --port ${port} --host 0.0.0.0`.quiet();
try {
  const deadline = Date.now() + 60_000;
  while (!(await listening())) {
    if (Date.now() > deadline) throw new Error(`playwright run-server did not listen on ${port} within 60s`);
    await Bun.sleep(250);
  }
  const run = Bun.spawn(["bunx", "playwright", "test", "visual", ...process.argv.slice(2)], {
    cwd: uiRoot,
    stdio: ["inherit", "inherit", "inherit"],
    env: { ...process.env, PW_TEST_CONNECT_WS_ENDPOINT: `ws://127.0.0.1:${port}/`, PW_TEST_CONNECT_EXPOSE_NETWORK: "<loopback>" },
  });
  process.exitCode = await run.exited;
} finally {
  await $`docker rm -f ${container}`.quiet().nothrow();
}
