import { createApp } from "./app";

const port = Number(process.env.PORT ?? 3000);

Bun.serve({ port, fetch: createApp().fetch });

console.log(JSON.stringify({ level: "info", msg: "repo-guardian-ui listening", port }));
