package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/donaldgifford/repo-guardian/internal/shadow"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

const cmdVerifyShadow = "verify-shadow"

// runVerifyShadow implements `repo-guardian migrate verify-shadow`
// (DESIGN-0026 § Cutover, OQ15): compare a v2 shadow run's findings with
// v1's rule_state and fail on any disagreement no documented divergence
// explains.
func runVerifyShadow(args []string) error {
	fs := flag.NewFlagSet(cmdMigrate+" "+cmdVerifyShadow, flag.ContinueOnError)

	v1DSN := fs.String("v1-dsn", "", "DSN of the v1 database (rule_state)")
	v2DSN := fs.String("v2-dsn", "", "DSN of the v2 shadow database (findings)")
	asJSON := fs.Bool("json", false, "print the report as JSON")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("%s: %w", cmdVerifyShadow, err)
	}

	if *v1DSN == "" || *v2DSN == "" {
		return fmt.Errorf("%s: both --v1-dsn and --v2-dsn are required", cmdVerifyShadow)
	}

	rep, err := verifyShadow(context.Background(), *v1DSN, *v2DSN)
	if err != nil {
		return fmt.Errorf("%s: %w", cmdVerifyShadow, err)
	}

	if err := writeShadowReport(os.Stdout, rep, *asJSON); err != nil {
		return fmt.Errorf("%s: writing report: %w", cmdVerifyShadow, err)
	}

	if !rep.OK() {
		return fmt.Errorf("%s: %d unexplained divergence(s)", cmdVerifyShadow, len(rep.Unexplained))
	}

	return nil
}

func verifyShadow(ctx context.Context, v1DSN, v2DSN string) (_ *shadow.Report, retErr error) {
	v1db, err := pgstore.OpenDB(v1DSN)
	if err != nil {
		return nil, err
	}

	defer func() { retErr = errors.Join(retErr, v1db.Close()) }()

	v2db, err := pgstore.OpenDB(v2DSN)
	if err != nil {
		return nil, err
	}

	defer func() { retErr = errors.Join(retErr, v2db.Close()) }()

	return shadow.Verify(ctx, v1db, v2db)
}

func writeShadowReport(w io.Writer, rep *shadow.Report, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		return enc.Encode(rep)
	}

	if _, err := fmt.Fprintf(w, "compared %d repositories (%d active in v1 only, %d in v2 only)\n",
		rep.Repositories, rep.OnlyV1, rep.OnlyV2); err != nil {
		return err
	}

	classes := make([]string, 0, len(rep.Counts))
	for c := range rep.Counts {
		classes = append(classes, string(c))
	}

	slices.Sort(classes)

	for _, c := range classes {
		if _, err := fmt.Fprintf(w, "  %-15s %d\n", c, rep.Counts[shadow.Class(c)]); err != nil {
			return err
		}
	}

	for _, p := range rep.Unexplained {
		v1, v2 := "no row", "no finding"
		if p.V1 != nil {
			v1 = fmt.Sprintf("actionable=%t", p.V1.Actionable)
		}

		if p.V2 != nil {
			v2 = fmt.Sprintf("%s/%s", p.V2.Status, p.V2.Reason)
		}

		if _, err := fmt.Fprintf(w, "unexplained %s/%s %s %q: v1 %s, v2 %s\n",
			p.Org, p.Repo, p.RuleKind, p.RuleName, v1, v2); err != nil {
			return err
		}
	}

	return nil
}
