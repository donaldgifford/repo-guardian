// Shared fixtures for the server's bun tests.

export const testKey = "k".repeat(32);

export const validEnv = {
  OIDC_ISSUER: "https://idp.example.com/realms/rg",
  OIDC_CLIENT_ID: "repo-guardian-ui",
  OIDC_CLIENT_SECRET: "client-secret",
  API_UPSTREAM: "http://repo-guardian-api:80",
  PUBLIC_URL: "https://guardian.example.com",
  UI_SESSION_KEYS: testKey,
};
