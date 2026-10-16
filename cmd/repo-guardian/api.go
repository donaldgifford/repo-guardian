package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/config"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// startAPI builds the read-only API (DESIGN-0027): the read-only pool,
// OIDC authentication with background discovery, and the authz file.
// It returns the handler and its readiness checks: the pool, the schema
// version and, with auth enabled, OIDC discovery.
//
// The status page refreshes in the background from the same pool; with a
// Temporal client (the api role beside the worker) it also probes the
// task-queue backlog.
func startAPI(
	ctx context.Context, cfg *config.Config, roles config.Role, tc client.Client, tcfg *temporal.Config, logger *slog.Logger,
) (http.Handler, []readinessCheck, error) {
	pool, err := pgstore.NewReadOnlyPool(ctx, cfg.APIStoreDSN(roles))
	if err != nil {
		return nil, nil, err
	}

	go func() {
		<-ctx.Done()
		pool.Close()
	}()

	reader := pgstore.NewAPIReader(pool)
	statusCfg := &api.StatusConfig{
		Reader: reader, CheckInterval: cfg.EvalInterval, DiscoveryInterval: cfg.DiscoveryInterval,
		SnapshotInterval: cfg.ComplianceSnapshotInterval, RateReserve: cfg.RateLimitThreshold, Logger: logger,
	}

	if tc != nil {
		statusCfg.Backlogs = backlogProbes(cfg, roles, tc, tcfg)
	}

	status := api.NewStatusPage(statusCfg)
	go status.Run(ctx)

	opts := &api.Options{
		Reader: reader, Status: status, StatusRequiresAuth: !cfg.API.StatusPublic, Logger: logger,
	}

	if tc != nil {
		opts.Rechecker = evaluationRechecker{signaler: tc}
	}
	checks := []readinessCheck{
		{name: "api_store", fn: pool.Ping},
		{name: "api_schema", fn: func(ctx context.Context) error {
			return pgstore.RequireControlsSchema(ctx, pool, pgstore.ControlsSchemaVersion)
		}},
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

// backlogProbes describes the queue of every controls half this process
// runs: a half it does not run has no workers here by design, and
// probing it would report the page down.
func backlogProbes(cfg *config.Config, roles config.Role, tc client.Client, tcfg *temporal.Config) []api.QueueProbe {
	probe := func(queue string) api.BacklogProbe {
		return func(ctx context.Context) (api.Backlog, error) {
			age, pollers, err := temporal.DescribeBacklog(ctx, tc, tcfg.Namespace, queue)

			return api.Backlog{Age: age, Pollers: pollers}, err
		}
	}

	var probes []api.QueueProbe

	if cfg.RunsControls(roles, config.RoleEvaluator) {
		probes = append(probes, api.QueueProbe{Component: api.ComponentBacklogEvaluation, Probe: probe(tcfg.EvalTaskQueue)})
	}

	if cfg.RunsControls(roles, config.RoleRemediator) {
		probes = append(probes, api.QueueProbe{Component: api.ComponentBacklogRemediation, Probe: probe(tcfg.RemediateTaskQueue)})
	}

	return probes
}

// signaler is the one Temporal call the re-check needs. The api role's
// client is used for this and DescribeTaskQueue alone: it never starts a
// workflow (DESIGN-0032 AR-0032-12).
type signaler interface {
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg any) error
}

// evaluationRechecker sends POST /repositories/{id}/evaluate's re-check:
// recheck at the human priority to evaluation/<id>, evaluating every
// control (Unknown), since a person asked for all of it.
type evaluationRechecker struct {
	signaler signaler
}

func (r evaluationRechecker) Recheck(ctx context.Context, repositoryID int64) error {
	err := r.signaler.SignalWorkflow(ctx, workflows.EvaluationWorkflowID(repositoryID), "", workflows.RecheckSignal,
		workflows.Recheck{Trigger: workflows.TriggerManual, Priority: workflows.PriorityHuman, Unknown: true})

	var missing *serviceerror.NotFound
	if errors.As(err, &missing) {
		return api.ErrWorkflowMissing
	}

	if err != nil {
		return fmt.Errorf("signal %s: %w", workflows.EvaluationWorkflowID(repositoryID), err)
	}

	return nil
}
