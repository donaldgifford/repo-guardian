package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/repo-guardian/tools/rgctl/internal/v1config"
)

const (
	cmdConfig   = "config"
	literalTrue = "true"

	formatTable = "table"
	formatJSON  = "json"
)

func (a *app) configCommand() *cobra.Command {
	cmd := &cobra.Command{Use: cmdConfig, Short: "Inspect a repo-guardian v1 policy file"}
	cmd.AddCommand(a.configShowCommand())
	return cmd
}

func (a *app) configShowCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "show <guardian.hcl>",
		Short: "Print what a v1 policy declares: orgs, guardian knobs, rules, PR templates, ignores",
		Long: "show reads the file without the v1 loader and never evaluates it: values are printed as written,\n" +
			"and any block or attribute it does not know is listed under unrecognised with its line.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if format != formatTable && format != formatJSON {
				return usageError(fmt.Errorf("--format must be %s or %s, got %q", formatTable, formatJSON, format))
			}
			doc, err := v1config.Read(args[0])
			if err != nil {
				return usageError(err)
			}
			if format == formatJSON {
				enc := json.NewEncoder(a.out)
				enc.SetIndent("", "  ")
				return enc.Encode(doc)
			}
			if len(doc.Unrecognised) > 0 {
				a.logger().Warn("policy has blocks or attributes rgctl does not recognise", "count", len(doc.Unrecognised))
			}
			return writeDocument(a.out, doc)
		},
	}
	cmd.Flags().StringVar(&format, "format", formatTable, "output: table or json")
	return cmd
}

// writeDocument renders doc as the sections of DESIGN-0035's reader table:
// orgs, guardian, rules, pr, ignore, unrecognised.
func writeDocument(w io.Writer, doc *v1config.Document) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(tw, format, args...) } //nolint:errcheck // tabwriter reports the first write error on Flush
	// section flushes the writer so each section gets its own column widths.
	section := func(title string) {
		_ = tw.Flush() //nolint:errcheck // a write error resurfaces on the final Flush
		p("\n%s\n", title)
	}

	p("POLICY\t%s\n", doc.Path)
	if doc.LegacyMode {
		p("ORGS\tevery installed org (legacy mode)\n")
	} else {
		p("ORGS\t%s\n", orNone(strings.Join(doc.Orgs, ", ")))
	}

	section("GUARDIAN")
	writeAttrs(p, doc.Guardian)
	if len(doc.Locals) > 0 {
		section("LOCALS")
		writeAttrs(p, doc.Locals)
	}

	section("RULES")
	p("  TYPE\tNAME\tENABLED\tCHECK\tPATHS\tTEMPLATE\tSCOPE\tIGNORE\tWHEN\tRECONCILERS\n")
	for i := range doc.Rules {
		r := &doc.Rules[i]
		p("  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Type, r.Name, enabled(r.Enabled), orDash(r.Check),
			orDash(strings.Join(r.Paths, ", ")), orDash(r.Template), orDash(strings.Join(r.Scope, ", ")),
			orDash(strings.Join(r.Ignore, ", ")), when(r.When), orDash(reconcilers(r.Reconcilers)))
	}
	section("RULE ATTRIBUTES")
	for i := range doc.Rules {
		r := &doc.Rules[i]
		for _, at := range r.Attrs {
			p("  %s.%s\t%s\n", r.Name, at.Name, oneLine(at.Source))
		}
		for _, at := range r.Assertions {
			p("  %s.assertion\t%s\n", r.Name, oneLine(at.Source))
		}
	}

	section("PR")
	if !hasPR(doc) {
		p("  (none)\n")
	}
	writePR(p, "defaults", doc.Defaults)
	for i := range doc.Rules {
		r := &doc.Rules[i]
		writePR(p, fmt.Sprintf("rule %s", r.Name), r.PR)
		for _, rec := range r.Reconcilers {
			writePR(p, fmt.Sprintf("rule %s > reconcile %s", r.Name, rec.Type), rec.PR)
		}
	}

	section("IGNORE")
	p("  %s\n", orNone(strings.Join(doc.Ignore, ", ")))

	section("UNRECOGNISED")
	if len(doc.Unrecognised) == 0 {
		p("  (none)\n")
	}
	for _, it := range doc.Unrecognised {
		p("  line %d\t%s %s\t%s\n", it.Line, it.Kind, it.Name, oneLine(it.Source))
	}
	return tw.Flush()
}

func writeAttrs(p func(string, ...any), attrs []v1config.Attr) {
	if len(attrs) == 0 {
		p("  (none)\n")
	}
	for _, at := range attrs {
		p("  %s\t%s\n", at.Name, oneLine(at.Source))
	}
}

func writePR(p func(string, ...any), where string, pr *v1config.PRBlock) {
	if pr == nil {
		return
	}
	for _, at := range pr.Attrs {
		p("  %s\t%s\t%s\n", where, at.Name, oneLine(at.Source))
	}
}

func hasPR(doc *v1config.Document) bool {
	if doc.Defaults != nil {
		return true
	}
	for i := range doc.Rules {
		if doc.Rules[i].PR != nil {
			return true
		}
		for _, rec := range doc.Rules[i].Reconcilers {
			if rec.PR != nil {
				return true
			}
		}
	}
	return false
}

func enabled(b *bool) string {
	if b == nil {
		return "-"
	}
	return strconv.FormatBool(*b)
}

func when(a *v1config.Attr) string {
	if a == nil {
		return "-"
	}
	return a.Source
}

func reconcilers(recs []v1config.Reconciler) string {
	names := make([]string, 0, len(recs))
	for _, r := range recs {
		name := r.Type
		for _, at := range r.Attrs {
			if at.Name == "watch" && at.Source == literalTrue {
				name += " (watch)"
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// oneLine shortens multi-line source (a heredoc body) to its first line plus
// a line count, so the table stays one row per item.
func oneLine(s string) string {
	first, rest, ok := strings.Cut(s, "\n")
	if !ok {
		return s
	}
	return fmt.Sprintf("%s … (+%d lines)", first, strings.Count(rest, "\n")+1)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
