package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/config"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// startAPI builds the read-only API (DESIGN-0027): the read-only pool,
// OIDC authentication with background discovery, and the authz file.
// It returns the handler and its readiness checks: the pool, the schema
// version and, with auth enabled, OIDC discovery.
func startAPI(ctx context.Context, cfg *config.Config, roles config.Role, logger *slog.Logger) (http.Handler, []readinessCheck, error) {
	pool, err := pgstore.NewReadOnlyPool(ctx, cfg.APIStoreDSN(roles))
	if err != nil {
		return nil, nil, err
	}

	go func() {
		<-ctx.Done()
		pool.Close()
	}()

	opts := &api.Options{Reader: pgstore.NewAPIReader(pool), StaleAfter: cfg.API.PRStaleAfter, Logger: logger}
	checks := []readinessCheck{
		{name: "api_store", fn: pool.Ping},
		{name: "api_schema", fn: func(ctx context.Context) error { return pgstore.RequireSchema(ctx, pool, pgstore.SchemaVersion) }},
	}

	if cfg.API.AuthEnabled {
		if opts.Authz, err = api.LoadAuthzConfig(cfg.API.AuthzConfigPath); err != nil {
			return nil, nil, err
		}

		opts.Authn = api.NewAuthenticator(&api.AuthnConfig{
			Issuer: cfg.API.OIDCIssuer, Audience: cfg.API.OIDCAudience,
			NameClaim: cfg.API.OIDCNameClaim, GroupsClaim: cfg.API.OIDCGroupsClaim,
		}, logger)

		go opts.Authn.Start(ctx)

		checks = append(checks, readinessCheck{name: "oidc", fn: func(context.Context) error {
			if !opts.Authn.Ready() {
				return errors.New("OIDC discovery has not succeeded")
			}

			return nil
		}})
	} else {
		logger.Warn("API AUTHENTICATION IS DISABLED: every caller sees every org. " +
			"Bind the API to the pod network only and render no Ingress (API_AUTH_ENABLED=false)")
	}

	h, err := api.New(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("build api: %w", err)
	}

	return h, checks, nil
}
