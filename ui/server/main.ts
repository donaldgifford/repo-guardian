import { createApp } from "./app";
import { ConfigError, loadConfig } from "./config";

function main(): void {
  let config;
  try {
    config = loadConfig(process.env);
  } catch (e) {
    if (e instanceof ConfigError) {
      console.error(JSON.stringify({ level: "error", msg: "invalid configuration", problems: e.problems }));
      process.exit(1);
    }
    throw e;
  }

  Bun.serve({ port: config.port, fetch: createApp({ config }).fetch });

  console.log(JSON.stringify({ level: "info", msg: "repo-guardian-ui listening", port: config.port }));
}

main();
