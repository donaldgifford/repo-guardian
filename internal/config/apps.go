package config

import (
	"fmt"
	"os"
	"strconv"
)

// App names one of the two controls GitHub Apps (DESIGN-0032 § Two
// Apps). The evaluation App reads; the remediation App writes.
type App string

// The two Apps, in the form every label, route and workflow id uses.
const (
	AppEval      App = "eval"
	AppRemediate App = "remediate"
)

// AppCredentials is one App's credential set. The private key is a
// path only: keys reach the process as mounted files, never as values.
type AppCredentials struct {
	// AppID is <PREFIX>_GITHUB_APP_ID.
	AppID int64

	// PrivateKeyPath is <PREFIX>_GITHUB_PRIVATE_KEY_PATH.
	PrivateKeyPath string

	// WebhookSecret is <PREFIX>_WEBHOOK_SECRET, the HMAC secret of this
	// App's webhook route.
	WebhookSecret string
}

// The variable prefixes of the two credential sets.
const (
	envPrefixEval      = "EVAL"
	envPrefixRemediate = "REMEDIATE"
)

// envPrefix is the variable prefix of a's credential set.
func (a App) envPrefix() string {
	if a == AppEval {
		return envPrefixEval
	}

	return envPrefixRemediate
}

// parseAppCredentials reads a's credential set.
func parseAppCredentials(a App) (AppCredentials, error) {
	p := a.envPrefix()

	creds := AppCredentials{
		PrivateKeyPath: os.Getenv(p + "_GITHUB_PRIVATE_KEY_PATH"),
		WebhookSecret:  os.Getenv(p + "_WEBHOOK_SECRET"),
	}

	if s := os.Getenv(p + "_GITHUB_APP_ID"); s != "" {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return AppCredentials{}, fmt.Errorf("parsing %s_GITHUB_APP_ID %q: %w", p, s, err)
		}

		creds.AppID = id
	}

	return creds, nil
}

// validateApp reports what a role that acts as a lacks: the App id and
// the key path. The webhook secret belongs to ingest and is checked
// there.
func (c *Config) validateApp(a App, role string) []error {
	creds, p := c.Credentials(a), a.envPrefix()

	var errs []error

	if creds.AppID == 0 {
		errs = append(errs, fmt.Errorf("%s_GITHUB_APP_ID is required for the %s role", p, role))
	}

	if creds.PrivateKeyPath == "" {
		errs = append(errs, fmt.Errorf("%s_GITHUB_PRIVATE_KEY_PATH is required for the %s role", p, role))
	}

	return errs
}

// Credentials returns a's credential set.
func (c *Config) Credentials(a App) AppCredentials {
	if a == AppEval {
		return c.EvalApp
	}

	return c.RemediateApp
}

// validateEvaluator checks what the evaluator role needs: the
// evaluation App, Temporal, the store and the policy.
func (c *Config) validateEvaluator() []error {
	return append(c.validateApp(AppEval, "evaluator"), c.validateControlsWorker("evaluator")...)
}

// validateRemediator checks what the remediator role needs: the
// remediation App, Temporal, the store and the policy.
func (c *Config) validateRemediator() []error {
	return append(c.validateApp(AppRemediate, "remediator"), c.validateControlsWorker("remediator")...)
}

// validateControlsWorker is what both controls worker roles share.
func (c *Config) validateControlsWorker(role string) []error {
	var errs []error

	if c.StoreDSN == "" {
		errs = append(errs, fmt.Errorf("STORE_DSN is required for the %s role", role))
	}

	if c.GuardianConfigPath == "" {
		errs = append(errs, fmt.Errorf("GUARDIAN_CONFIG is required for the %s role", role))
	}

	return errs
}
