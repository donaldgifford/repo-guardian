// docker-bake.hcl — single source of truth for all Docker image builds.
//
// Targets:
//   dev     — local single-arch build, loads into Docker daemon
//   ci      — multi-arch validation build, no push
//   release — multi-arch build, pushes to registry

variable "REGISTRY" {
  default = "ghcr.io"
}

variable "IMAGE_NAME" {
  default = "donaldgifford/repo-guardian"
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
  params = [version]
  result = version == "dev" ? [
    "${REGISTRY}/${IMAGE_NAME}:dev",
  ] : [
    "${REGISTRY}/${IMAGE_NAME}:${version}",
    "${REGISTRY}/${IMAGE_NAME}:latest",
  ]
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
  tags      = tags("dev")
  output    = ["type=docker"]
}

// CI validation build — multi-arch, no push.
target "ci" {
  inherits  = ["_common"]
  tags      = tags(VERSION)
  platforms = ["linux/amd64", "linux/arm64"]
  output    = ["type=cacheonly"]
  cache-from = ["type=gha"]
  cache-to   = ["type=gha,mode=max"]
}

// Release build — multi-arch, pushes to registry.
// In CI, docker/metadata-action overrides tags via the bake file merge pattern.
target "release" {
  inherits  = ["_common"]
  tags      = tags(VERSION)
  platforms = ["linux/amd64", "linux/arm64"]
  output    = ["type=registry"]
  cache-from = ["type=gha"]
  cache-to   = ["type=gha,mode=max"]
}
