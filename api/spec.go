// Package api embeds the OpenAPI contract, api/openapi.yaml, so the
// server can publish it byte for byte at /api/v1/openapi.yaml.
package api

import _ "embed"

// Spec is api/openapi.yaml as written.
//
//go:embed openapi.yaml
var Spec []byte
