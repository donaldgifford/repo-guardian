package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

const cmdVersion = "version"

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   cmdVersion,
		Short: "Print build information",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			_, err := fmt.Fprintln(a.out, versionString())
			return err
		},
	}
}

// versionString renders the module path, version and VCS state from the
// build info. A `go install ...@main` build carries a pseudo-version; a build
// from a checkout carries the revision and dirty flag.
func versionString() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "rgctl (no build info)"
	}
	s := fmt.Sprintf("rgctl %s %s", bi.Main.Path, bi.Main.Version)
	var rev, modified string
	for _, kv := range bi.Settings {
		switch kv.Key {
		case "vcs.revision":
			rev = kv.Value
		case "vcs.modified":
			modified = kv.Value
		}
	}
	if rev != "" {
		s += " revision " + rev
		if modified == literalTrue {
			s += " (dirty)"
		}
	}
	return s + " " + bi.GoVersion
}
