# syntax=docker/dockerfile:1.26@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Cache dependencies.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build.
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
  go build -trimpath -ldflags="-s -w" -o /repo-guardian ./cmd/repo-guardian

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /repo-guardian /repo-guardian

EXPOSE 8080 9090

USER nonroot:nonroot

# No Dockerfile HEALTHCHECK: the distroless/static base ships no
# shell, curl, or wget, so an HTTP probe directive can't run inside
# the image. Production runs on k8s where the Helm chart configures
# livenessProbe/readinessProbe against /healthz and /readyz directly.
# Setting NONE explicitly so Docker doesn't sit in `health: starting`
# forever when run via plain `docker run`.
HEALTHCHECK NONE

ENTRYPOINT ["/repo-guardian"]
