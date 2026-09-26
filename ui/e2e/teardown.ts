import { execFileSync } from "node:child_process";

// Playwright may stop the stack before its own teardown has removed
// the Postgres container; sweep anything carrying the e2e label.
export default function teardown() {
  const ids = execFileSync("docker", ["ps", "-aq", "--filter", "label=repo-guardian-e2e"], { encoding: "utf8" }).trim().split("\n").filter(Boolean);
  if (ids.length > 0) {
    execFileSync("docker", ["rm", "-f", ...ids], { stdio: "ignore" });
  }
}
