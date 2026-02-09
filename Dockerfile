# Build stage
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

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

ENTRYPOINT ["/repo-guardian"]
