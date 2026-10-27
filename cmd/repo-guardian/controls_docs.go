package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
)

// Subcommand and verb names for `repo-guardian controls <verb>`.
const (
	cmdControls = "controls"
	verbDocs    = "docs"
)

// runControls implements `repo-guardian controls <verb>`; the verb is
// switched on before any FlagSet parses, as in runMonitoring.
func runControls(args []string) error {
	if len(args) == 0 || args[0] != verbDocs {
		return errors.New("controls: want `repo-guardian controls docs [--out FILE]`")
	}

	fs := flag.NewFlagSet("controls docs", flag.ContinueOnError)
	out := fs.String("out", "", "write the reference to FILE (default stdout)")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if *out == "" {
		return builtin.WriteReference(os.Stdout)
	}

	return writeFile(*out, builtin.WriteReference)
}

func writeFile(path string, render func(io.Writer) error) error {
	f, err := os.Create(path) //nolint:gosec // the operator names the output file
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}

	if err := render(f); err != nil {
		f.Close() //nolint:errcheck,gosec // the render error is the one to report

		return err
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	return nil
}
