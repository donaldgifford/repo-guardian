import * as client from "openid-client";

import { type Config, isLoopback } from "./config";

// Oidc holds the issuer's discovered configuration. Discovery is lazy and
// retried: a failed attempt is forgotten so the next request tries again,
// and readiness reports false until one succeeds.
export class Oidc {
  private pending: Promise<client.Configuration> | undefined;
  private loaded: client.Configuration | undefined;

  constructor(private readonly config: Config) {}

  // configuration returns the discovered configuration, discovering it on
  // first use.
  configuration(): Promise<client.Configuration> {
    if (this.loaded) {
      return Promise.resolve(this.loaded);
    }

    this.pending ??= this.discover().then(
      (c) => {
        this.loaded = c;
        return c;
      },
      (err: unknown) => {
        this.pending = undefined;
        throw err;
      },
    );

    return this.pending;
  }

  // ready reports whether discovery has succeeded.
  ready(): boolean {
    return this.loaded !== undefined;
  }

  private discover(): Promise<client.Configuration> {
    const { issuer, clientId, clientSecret, tokenAuthMethod } = this.config.oidc;
    // Plain http is only ever accepted for a loopback issuer (config
    // validation rejects it elsewhere): local development and tests.
    const execute = issuer.protocol === "http:" && isLoopback(issuer) ? [client.allowInsecureRequests] : [];

    // The method is the operator's (OIDC_TOKEN_AUTH_METHOD): post is the
    // default, basic is what some providers' confidential clients require.
    const auth =
      tokenAuthMethod === "client_secret_basic" ? client.ClientSecretBasic(clientSecret) : client.ClientSecretPost(clientSecret);

    return client.discovery(issuer, clientId, clientSecret, auth, { execute });
  }
}
