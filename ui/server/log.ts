// Logger writes one JSON line per event, matching the Go binary's shape.
export type Logger = (level: "info" | "warn" | "error", msg: string, attrs?: Record<string, unknown>) => void;

export const jsonLogger: Logger = (level, msg, attrs = {}) => {
  const line = JSON.stringify({ time: new Date().toISOString(), level, msg, ...attrs });
  if (level === "info") {
    console.log(line);
  } else {
    console.error(line);
  }
};

export const discardLogger: Logger = () => {};
