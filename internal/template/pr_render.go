package template

import (
	"fmt"
	"strings"
	"sync"
)

// MaxPRBodyChars bounds a rendered PR body below GitHub's 65,536
// character limit; a longer body is cut and ends in TruncatedMarker.
const MaxPRBodyChars = 65000

// TruncatedMarker ends a PR body that was cut to MaxPRBodyChars.
const TruncatedMarker = "\n\n<!-- repo-guardian:truncated -->\n_This description was truncated._\n"

// The built-in remediation PR title and body (IMPL-0030 OQ10); a
// control's pr {} block overrides each field by field.
const (
	DefaultPRTitle = `chore: {{ .Control.Title }}`
	DefaultPRBody  = `This pull request was opened by repo-guardian for the ` + "`{{ .Control.ID }}`" + ` control.
{{ if .Fixed }}
## What this fixes
{{ range .Fixed }}
- ` + "`{{ .ID }}`" + `{{ if .Title }}: {{ .Title }}{{ end }}{{ end }}
{{ end }}{{ if .Manual }}
## What needs a human
{{ range .Manual }}
- ` + "`{{ .ID }}`" + `{{ if .Title }}: {{ .Title }}{{ end }}{{ end }}
{{ end }}{{ if .Notes }}
## Notes
{{ range .Notes }}
- {{ . }}{{ end }}
{{ end }}{{ if .ControlsURL }}
See the repository's controls: {{ .ControlsURL }}
{{ end }}`
)

var (
	defaultsOnce              sync.Once
	defaultTitle, defaultBody *Compiled
	errDefaults               error
)

// defaultPR compiles the built-in title and body once.
func defaultPR() (title, body *Compiled, err error) {
	defaultsOnce.Do(func() {
		r := NewRenderer()

		if defaultTitle, errDefaults = r.Parse("pr-title", DefaultPRTitle); errDefaults != nil {
			return
		}

		defaultBody, errDefaults = r.Parse("pr-body", DefaultPRBody)
	})

	return defaultTitle, defaultBody, errDefaults
}

// RenderedPR is a remediation PR's title, body and labels.
type RenderedPR struct {
	Title  string
	Body   string
	Labels []string
}

// RenderPR renders a remediation PR from a control's pr {} block,
// falling back to the built-in title and body field by field, and cuts
// the body to MaxPRBodyChars with TruncatedMarker.
func RenderPR(cfg *PRConfig, vars *PRVars) (RenderedPR, error) {
	title, body, err := defaultPR()
	if err != nil {
		return RenderedPR{}, fmt.Errorf("built-in pr template: %w", err)
	}

	var labels []string

	if cfg != nil {
		if cfg.CompiledTitle != nil {
			title = cfg.CompiledTitle
		}

		if cfg.CompiledBody != nil {
			body = cfg.CompiledBody
		}

		labels = cfg.Labels
	}

	t, err := title.Render(vars)
	if err != nil {
		return RenderedPR{}, fmt.Errorf("pr title: %w", err)
	}

	b, err := body.Render(vars)
	if err != nil {
		return RenderedPR{}, fmt.Errorf("pr body: %w", err)
	}

	return RenderedPR{Title: strings.TrimSpace(t), Body: truncateBody(b), Labels: labels}, nil
}

// truncateBody cuts b to MaxPRBodyChars characters (runes), marker
// included, never splitting a rune.
func truncateBody(b string) string {
	runes := []rune(b)
	if len(runes) <= MaxPRBodyChars {
		return b
	}

	keep := MaxPRBodyChars - len([]rune(TruncatedMarker))

	return string(runes[:keep]) + TruncatedMarker
}
