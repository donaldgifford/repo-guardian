package main

import (
	"errors"
	"strconv"
)

// Exit codes, shared by every command (DESIGN-0035 "Output and exit codes").
const (
	exitOK          = 0 // nothing found, or every planned action done
	exitFound       = 1 // open repo-guardian PRs found (list), or PRs skipped as edited (close)
	exitUsage       = 2 // usage or configuration error
	exitOperational = 3 // auth, rate limit exhausted, API error
)

// exitError carries a command's exit code. A nil err is a silent non-zero
// exit: the command has already said what it found.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return "exit status " + strconv.Itoa(e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// usageError marks err as a usage or configuration error (exit 2).
func usageError(err error) error { return &exitError{code: exitUsage, err: err} }

// opError marks err as an operational failure (exit 3).
func opError(err error) error { return &exitError{code: exitOperational, err: err} }

// exitCode maps a command's error to the process exit code. Errors that are
// not an *exitError come from cobra itself (unknown command, bad flag, wrong
// argument count), so they are usage errors.
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	if ee, ok := errors.AsType[*exitError](err); ok {
		return ee.code
	}
	return exitUsage
}
