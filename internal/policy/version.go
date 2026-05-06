package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Version returns a deterministic hash of the loaded policy plus the
// raw template content backing it, used by the sweep loop to detect
// when a repo's last reconcile predates the current policy and
// therefore must be re-checked.
//
// The hash composition (per IMPL-0011 Phase 1):
//  1. JSON-serialize cfg via encoding/json. The stdlib serializes
//     map keys in sorted order, giving us canonical bytes for the
//     policy-config shape. Env-var overrides applied in
//     loader.applyEnvOverrides land in cfg before this call, so they
//     influence the hash naturally.
//  2. Append every (template name, content) pair in sorted-by-name
//     order. Template content drives file-rule output and PR text;
//     a change there must invalidate cached freshness.
//  3. SHA-256 the concatenation; hex-encode for human-readable
//     storage in Postgres `repo_state.policy_version`.
//
// Compiled template pointers on PRConfig (CompiledTitle/CompiledBody)
// have only unexported fields and so contribute nothing to the JSON
// output — the hash is stable across runs that compile the same
// source bytes.
func Version(cfg *PolicyConfig, templates map[string]string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("policy.Version: nil config")
	}

	h := sha256.New()

	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("policy.Version: marshal config: %w", err)
	}

	if _, err := h.Write(cfgBytes); err != nil {
		return "", fmt.Errorf("policy.Version: hash config: %w", err)
	}

	if _, err := h.Write([]byte{0}); err != nil { // separator
		return "", fmt.Errorf("policy.Version: hash separator: %w", err)
	}

	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		if _, err := fmt.Fprintf(h, "%s\x00%s\x00", name, templates[name]); err != nil {
			return "", fmt.Errorf("policy.Version: hash template %q: %w", name, err)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
