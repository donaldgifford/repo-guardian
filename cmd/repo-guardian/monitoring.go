package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/emit"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// Output formats, aliased from the emit package so the flag help and
// the emitter cannot disagree about what is accepted.
const (
	formatJSON = emit.FormatJSON
	formatK8s  = emit.FormatK8s
)

// Subcommand and verb names, shared with dispatch and the usage text.
const (
	cmdHelp       = "help"
	cmdReport     = "report"
	cmdMonitoring = "monitoring"
	verbGenerate  = "generate"
)

// runMonitoring implements `repo-guardian monitoring <verb>`.
//
// Switches on the verb BEFORE building a FlagSet, for the reason in
// dispatch's doc comment: the flag package stops at the first non-flag
// argument, so parsing first would swallow the verb and leave its flags
// in a tail nobody reads.
func runMonitoring(args []string) error {
	if len(args) == 0 {
		monitoringUsage(os.Stderr)

		return errors.New("monitoring: no subcommand; try `repo-guardian monitoring generate`")
	}

	switch args[0] {
	case verbGenerate:
		return runMonitoringGenerate(args[1:])
	case cmdHelp:
		monitoringUsage(os.Stdout)

		return nil
	default:
		monitoringUsage(os.Stderr)

		return fmt.Errorf("monitoring: unknown subcommand %q", args[0])
	}
}

// monitoringUsage lists the monitoring verbs.
//
//nolint:errcheck // usage text; no recovery path, same as usage()
func monitoringUsage(w io.Writer) {
	fmt.Fprint(w, `repo-guardian monitoring — generate dashboards and alerts from the policy

Usage:
  repo-guardian monitoring generate [flags]   emit monitoring artifacts
  repo-guardian monitoring help               show this message

Artifacts are derived from the same guardian.hcl the server loads, so a
panel cannot outlive the rule it charts.
`)
}

// orgList collects a repeatable --org flag.
type orgList []string

func (o *orgList) String() string { return strings.Join(*o, ",") }

func (o *orgList) Set(v string) error {
	if v == "" {
		return errors.New("empty org")
	}

	*o = append(*o, v)

	return nil
}

// labelMap collects a repeatable key=value flag.
type labelMap map[string]string

func (m labelMap) String() string {
	pairs := make([]string, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, k+"="+v)
	}

	sort.Strings(pairs)

	return strings.Join(pairs, ",")
}

func (m labelMap) Set(v string) error {
	key, val, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return fmt.Errorf("want key=value, got %q", v)
	}

	m[key] = val

	return nil
}

// generateFlags is the parsed command line.
type generateFlags struct {
	config string
	out    string
	format string
	orgs   orgList

	prometheusUID string
	lokiUID       string
	lokiSelector  string

	namespace        string
	name             string
	instanceSelector labelMap
	labels           labelMap
	crossNamespace   bool
	resyncPeriod     string
}

// parseGenerateFlags builds and parses the flag set.
func parseGenerateFlags(args []string) (*generateFlags, error) {
	fs := flag.NewFlagSet(cmdMonitoring+" "+verbGenerate, flag.ContinueOnError)

	f := &generateFlags{
		instanceSelector: labelMap{},
		labels:           labelMap{},
	}

	fs.StringVar(&f.config, "config", os.Getenv("GUARDIAN_CONFIG"),
		"path to guardian.hcl (defaults to $GUARDIAN_CONFIG; built-in defaults when empty)")
	fs.StringVar(&f.out, "out", "./monitoring", "directory to write artifacts into")
	fs.StringVar(&f.format, "format", formatJSON, "output format: json|k8s")
	fs.Var(&f.orgs, "org",
		"org to generate a row for, repeatable; the escape hatch for configs with no top-level scope block")

	fs.StringVar(&f.prometheusUID, "prometheus-uid", dashboard.DefaultPrometheusUID,
		"uid of the Prometheus datasource the panels query")
	fs.StringVar(&f.lokiUID, "loki-uid", dashboard.DefaultLokiUID,
		"uid of the Loki datasource the log panels query")
	fs.StringVar(&f.lokiSelector, "loki-selector", dashboard.DefaultLogStream,
		"Loki stream selector matching repo-guardian's logs, without braces, e.g. job=\"ns/repo-guardian\"")

	fs.StringVar(&f.namespace, "namespace", "", "namespace to stamp on generated Kubernetes objects ("+formatK8s+" only)")
	fs.StringVar(&f.name, "name", emit.DefaultName, "base name for generated Kubernetes objects ("+formatK8s+" only)")
	fs.Var(f.instanceSelector, "instance-selector",
		"key=value matchLabels naming the Grafana instance to file dashboards into, repeatable ("+formatK8s+" only)")
	fs.Var(f.labels, "label", "key=value label to add to every generated object, repeatable ("+formatK8s+" only)")
	fs.BoolVar(&f.crossNamespace, "allow-cross-namespace-import", false,
		"let the operator file dashboards into a Grafana in another namespace ("+formatK8s+" only)")
	fs.StringVar(&f.resyncPeriod, "resync-period", "",
		"how often the operator re-applies a dashboard, e.g. 10m ("+formatK8s+" only)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// emit.Generate rejects an unknown format too, but only after the
	// policy has been loaded and the model derived. Checking here means
	// a typo'd --format reports the typo rather than whatever the
	// config file happens to say first.
	if f.format != formatJSON && f.format != formatK8s {
		return nil, fmt.Errorf("unknown --format %q; want %s or %s", f.format, formatJSON, formatK8s)
	}

	return f, nil
}

// runMonitoringGenerate implements `repo-guardian monitoring generate`.
//
// Deliberately does NOT call config.Load(), for the reason recorded on
// runReport: Load demands App credentials, a webhook secret and a
// Valkey DSN, none of which generating a dashboard from a config file
// requires.
func runMonitoringGenerate(args []string) error {
	f, err := parseGenerateFlags(args)
	if err != nil {
		// -h is a request, not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("monitoring generate: %w", err)
	}

	logger := initLoggerTo(os.Stderr, os.Getenv("LOG_LEVEL"))

	model, err := deriveModel(f)
	if err != nil {
		return err
	}

	warnUndeclarableOrgs(model, logger)

	ds := dashboard.Datasources{
		Prometheus: f.prometheusUID,
		Loki:       f.lokiUID,
		LogStream:  f.lokiSelector,
	}.WithDefaults()

	artifacts, err := emit.Generate(model, dashboard.Suite(model, ds), &emit.Options{
		Format:                    f.format,
		Name:                      f.name,
		Namespace:                 f.namespace,
		Labels:                    f.labels,
		InstanceSelector:          f.instanceSelector,
		AllowCrossNamespaceImport: f.crossNamespace,
		ResyncPeriod:              f.resyncPeriod,
	})
	if err != nil {
		return err
	}

	paths, err := emit.Write(f.out, artifacts)
	if err != nil {
		return err
	}

	logger.Info("monitoring artifacts generated",
		"config", describeConfig(f.config),
		"strict_scope", model.Strict,
		"orgs", len(model.Orgs),
		"rules", len(model.Rules),
		"mechanisms", model.Mechanisms.Sorted(),
		"env_influence", model.Source.EnvInfluence,
		"format", f.format,
		"out", f.out,
		"files", len(paths),
	)

	// The path list goes to stdout so it can be piped; every diagnostic
	// above it went to stderr. Same split as `report`.
	for _, p := range paths {
		if _, err := fmt.Fprintln(os.Stdout, p); err != nil {
			return fmt.Errorf("monitoring generate: write path list: %w", err)
		}
	}

	return nil
}

// deriveModel loads the policy and derives the monitoring model.
func deriveModel(f *generateFlags) (*monitoring.Model, error) {
	if err := requireConfigExists(f.config); err != nil {
		return nil, err
	}

	cfg, err := policy.Load(f.config)
	if err != nil {
		return nil, fmt.Errorf("monitoring generate: %w", err)
	}

	model, err := monitoring.Derive(cfg, monitoring.Options{
		ConfigPath: f.config,
		ExtraOrgs:  f.orgs,
	})
	if err != nil {
		return nil, err
	}

	return model, nil
}

// requireConfigExists refuses an explicitly-given path that is not
// there.
//
// policy.Load treats a missing file as "use built-in defaults" and
// returns nil error, which is right for the server (an operator who
// mounts no config wants the defaults) and wrong here in two ways. A
// typo would emit the default artifacts with exit 0, so `monitoring
// generate --config guardain.hcl` silently produces a dashboard for a
// policy nobody runs. And it defeats the generator-as-validation
// property task 5.6 documents: the validation only happens on the file
// path, so the one invocation that skips the file also skips Validate,
// validateStrictScope and compilePolicyTemplates.
//
// An EMPTY path stays legal and still means built-in defaults — that is
// how the static tier is generated.
func requireConfigExists(path string) error {
	if path == "" {
		return nil
	}

	// The path is a command-line flag from the operator running the
	// binary, who can already read anything this process can, and
	// policy.Load opens the very same path a few lines later. There is
	// no boundary here to traverse.
	//
	//nolint:gosec // G703: operator-supplied CLI path, not untrusted input
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("monitoring generate: --config %s: %w", path, err)
	}

	return nil
}

// describeConfig names the config source for the log line.
func describeConfig(path string) string {
	if path == "" {
		return "(built-in defaults)"
	}

	return path
}

// warnUndeclarableOrgs says out loud when the per-org rows cannot be
// declared from the config.
//
// Two distinct cases, both of which cost the silent-org signal — a row
// that renders empty because an org stopped reporting. Losing it
// quietly is the failure this whole design exists to avoid, so it is a
// warning rather than a debug line.
func warnUndeclarableOrgs(m *monitoring.Model, logger *slog.Logger) {
	if len(m.Orgs) == 0 {
		logger.Warn("no orgs declared; per-org rows will be discovered from series rather than configured",
			"reason", "the policy has no top-level scope block and no --org was given",
			"consequence", "an org that stops reporting disappears instead of rendering an empty row",
			"fix", "add a scope { orgs = [...] } block, or pass --org")

		return
	}

	var patterns []string

	for _, o := range m.Orgs {
		if o.Pattern {
			patterns = append(patterns, o.Name)
		}
	}

	if len(patterns) > 0 {
		logger.Warn("some scope orgs are glob patterns and cannot be enumerated",
			"patterns", patterns,
			"consequence", "those orgs get a discovered row, so a silent org among them is invisible",
			"fix", "name the orgs literally, or pass --org for the ones that must always render")
	}
}
