// Command rgctl inspects a repo-guardian v1 policy file and finds or closes
// the pull requests repo-guardian opened. It is its own Go module and imports
// nothing from the product module, so it keeps building after v1 is deleted.
//
// Usage:
//
//	rgctl config show guardian.hcl
//	rgctl prs list --org acme --format json --out acme.json
//	rgctl prs close --from acme.json --yes
//	rgctl version
//
// See docs/operations/rgctl.md and DESIGN-0035.
package main

import (
	"errors"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes rgctl with args and returns the process exit code. It never
// calls os.Exit, so tests can drive it.
func run(args []string, stdout, stderr io.Writer) int {
	return newApp(stdout, stderr).run(args)
}

// run executes the command tree with args against a's streams and hooks.
func (a *app) run(args []string) int {
	root := a.rootCommand()
	root.SetArgs(args)

	err := root.Execute()
	if hasMessage(err) {
		a.logger().Error(err.Error())
	}
	return exitCode(err)
}

// hasMessage reports whether err should be printed. A bare exitError with no
// cause is a silent exit: the command has already reported what it found.
func hasMessage(err error) bool {
	if err == nil {
		return false
	}
	if ee, ok := errors.AsType[*exitError](err); ok {
		return ee.err != nil
	}
	return true
}
