package clog

import (
	"bytes"
	"strings"
	"testing"
)

func TestNew_NoColorFlag(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lg, err := New(&buf, Options{NoColor: true})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	lg.Error("plain", "key", "value")
	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("New(NoColor) wrote escape sequences: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "plain") || !strings.Contains(buf.String(), "key=value") {
		t.Errorf("New(NoColor) output = %q, want message and attribute", buf.String())
	}
}

func TestNew_NoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	lg, err := New(&buf, Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	lg.Error("plain")
	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("New() with NO_COLOR wrote escape sequences: %q", buf.String())
	}
}

func TestNew_Level(t *testing.T) {
	t.Parallel()
	tests := []struct {
		level     string
		wantDebug bool
		wantInfo  bool
	}{
		{level: "", wantDebug: false, wantInfo: true},
		{level: "debug", wantDebug: true, wantInfo: true},
		{level: "warn", wantDebug: false, wantInfo: false},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			lg, err := New(&buf, Options{Level: tt.level, NoColor: true})
			if err != nil {
				t.Fatalf("New(%q) error = %v", tt.level, err)
			}
			lg.Debug("debug-line")
			lg.Info("info-line")
			if got := strings.Contains(buf.String(), "debug-line"); got != tt.wantDebug {
				t.Errorf("New(%q) debug logged = %v, want %v", tt.level, got, tt.wantDebug)
			}
			if got := strings.Contains(buf.String(), "info-line"); got != tt.wantInfo {
				t.Errorf("New(%q) info logged = %v, want %v", tt.level, got, tt.wantInfo)
			}
		})
	}
}

func TestNew_BadLevel(t *testing.T) {
	t.Parallel()
	if _, err := New(&bytes.Buffer{}, Options{Level: "loud"}); err == nil {
		t.Error(`New(Level: "loud") error = nil, want an error`)
	}
}
