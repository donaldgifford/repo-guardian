// docker-bake.hcl — single source of truth for all Docker image builds.
//
// Targets:
//   dev     — local single-arch build, loads into Docker daemon
//   ci      — multi-arch validation build, no push
//   release — multi-arch build, pushes to registry
//   ui-dev, ui-ci, ui-release — the same three for the UI image (ui/)

variable "REGISTRY" {
  default = "ghcr.io"
}

variable "IMAGE_NAME" {
  default = "donaldgifford/repo-guardian"
}

variable "UI_IMAGE_NAME" {
  default = "donaldgifford/repo-guardian-ui"
}

variable "VERSION" {
  default = "dev"
}

variable "COMMIT_SHA" {
  default = ""
}

variable "BUILD_DATE" {
  default = ""
}

function "tags" {
  params = [image, version]
  result = version == "dev" ? [
    "${REGISTRY}/${image}:dev",
  ] : concat(
    ["${REGISTRY}/${image}:${version}"],
    // Pre-releases (any "-" suffix, e.g. 2.0.0-rc.1) never move latest.
    length(regexall("-", version)) > 0 ? [] : ["${REGISTRY}/${image}:latest"],
  )
}

// Base target with shared configuration.
target "_common" {
  dockerfile = "Dockerfile"
  context    = "."
  labels = {
    "org.opencontainers.image.source"   = "https://github.com/donaldgifford/repo-guardian"
    "org.opencontainers.image.revision" = "${COMMIT_SHA}"
    "org.opencontainers.image.created"  = "${BUILD_DATE}"
    "org.opencontainers.image.version"  = "${VERSION}"
  }
}

// Local development build — single-arch, loads into Docker daemon.
target "dev" {
  inherits  = ["_common"]
  tags      = tags(IMAGE_NAME, "dev")
  output    = ["type=docker"]
}

// CI validation build — multi-arch, no push.
target "ci" {
  inherits  = ["_common"]
  tags      = tags(IMAGE_NAME, VERSION)
  platforms = ["linux/amd64", "linux/arm64"]
  output    = ["type=cacheonly"]
  cache-from = ["type=gha"]
  cache-to   = ["type=gha,mode=max"]
}

// Populated by docker/metadata-action in CI with computed tags and labels.
// Default tags are used for local `make docker-push`; CI overrides via bake file merge.
target "docker-metadata-action" {
  tags = tags(IMAGE_NAME, VERSION)
}

// Release build — multi-arch, pushes to registry.
// Tags are inherited from docker-metadata-action (overridden by metadata-action in CI).
target "release" {
  inherits  = ["_common", "docker-metadata-action"]
  platforms = ["linux/amd64", "linux/arm64"]
  output    = ["type=registry"]
  cache-from = ["type=gha"]
  cache-to   = ["type=gha,mode=max"]
}

// The UI image (DESIGN-0027): its own context and Dockerfile, the same
// labels and tag scheme, published beside the Go image under the same tag.
target "_ui" {
  inherits   = ["_common"]
  context    = "ui"
  dockerfile = "Dockerfile"
}

target "ui-dev" {
  inherits = ["_ui"]
  tags     = tags(UI_IMAGE_NAME, "dev")
  output   = ["type=docker"]
}

target "ui-ci" {
  inherits   = ["_ui"]
  tags       = tags(UI_IMAGE_NAME, VERSION)
  platforms  = ["linux/amd64", "linux/arm64"]
  output     = ["type=cacheonly"]
  cache-from = ["type=gha,scope=ui"]
  cache-to   = ["type=gha,mode=max,scope=ui"]
}

// Populated by docker/metadata-action (bake-target: docker-metadata-action-ui).
target "docker-metadata-action-ui" {
  tags = tags(UI_IMAGE_NAME, VERSION)
}

target "ui-release" {
  inherits   = ["_ui", "docker-metadata-action-ui"]
  platforms  = ["linux/amd64", "linux/arm64"]
  output     = ["type=registry"]
  cache-from = ["type=gha,scope=ui"]
  cache-to   = ["type=gha,mode=max,scope=ui"]
}
