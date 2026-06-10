# Build stage
FROM golang:1.22 AS builder

WORKDIR /workspace

# Copy go mod files
COPY go.mod go.sum ./

# Copy the local api module (for replace directive)
# In CI, this should be adjusted to use the published module
COPY api/ ../api/

RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/

# Build both binaries
RUN CGO_ENABLED=0 GOOS=linux go build -a -o pm-controller-kubernetes ./cmd/kubernetes/
RUN CGO_ENABLED=0 GOOS=linux go build -a -o pm-controller-aggregator ./cmd/aggregator/

# Runtime stage - Kubernetes provider
FROM gcr.io/distroless/static:nonroot AS kubernetes

WORKDIR /
COPY --from=builder /workspace/pm-controller-kubernetes .
USER 65532:65532

ENTRYPOINT ["/pm-controller-kubernetes"]

# Runtime stage - Aggregator
FROM gcr.io/distroless/static:nonroot AS aggregator

WORKDIR /
COPY --from=builder /workspace/pm-controller-aggregator .
USER 65532:65532

ENTRYPOINT ["/pm-controller-aggregator"]
