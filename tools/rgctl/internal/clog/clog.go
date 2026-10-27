// Package clog builds rgctl's human-facing logger: a [log/slog] logger backed
// by charmbracelet/log's handler, levelled, timestamped and coloured when the
// destination is a terminal.
package clog

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/charmbracelet/log"
	"github.com/muesli/termenv"
)

// Options configures [New].
type Options struct {
	// Level is a level name: debug, info, warn or error. Empty means info.
	Level string
	// NoColor disables colour even on a terminal. The NO_COLOR environment
	// variable has the same effect.
	NoColor bool
}

// New returns a logger writing to w. Colour follows the terminal unless
// opts.NoColor or NO_COLOR is set.
func New(w io.Writer, opts Options) (*slog.Logger, error) {
	level := log.InfoLevel
	if opts.Level != "" {
		parsed, err := log.ParseLevel(opts.Level)
		if err != nil {
			return nil, fmt.Errorf("log level %q: %w", opts.Level, err)
		}
		level = parsed
	}

	l := log.NewWithOptions(w, log.Options{
		ReportTimestamp: true,
		Level:           level,
	})
	if opts.NoColor || noColorEnv() {
		l.SetColorProfile(termenv.Ascii)
	}
	return slog.New(l), nil
}

// noColorEnv reports whether NO_COLOR is set to any non-empty value, per
// https://no-color.org.
func noColorEnv() bool {
	return os.Getenv("NO_COLOR") != ""
}
