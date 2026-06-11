# Build stage
FROM golang:1.22 AS builder

WORKDIR /workspace

# Copy go mod files (context is repo root)
COPY controller/go.mod controller/go.sum ./

# Copy the local api module (used via replace directive)
COPY api/ /api/

RUN go mod download

# Copy source code
COPY controller/cmd/ cmd/
COPY controller/internal/ internal/
COPY controller/pkg/ pkg/

# Build all controller binaries
RUN CGO_ENABLED=0 GOOS=linux go build -a -o pm-controller-kubernetes ./cmd/kubernetes/
RUN CGO_ENABLED=0 GOOS=linux go build -a -o pm-controller-aggregator ./cmd/aggregator/
RUN CGO_ENABLED=0 GOOS=linux go build -a -o pm-controller-github ./cmd/github/
RUN CGO_ENABLED=0 GOOS=linux go build -a -o pm-controller-greenhouse-teams ./cmd/greenhouse-teams/

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

# Runtime stage - GitHub provider
FROM gcr.io/distroless/static:nonroot AS github

WORKDIR /
COPY --from=builder /workspace/pm-controller-github .
USER 65532:65532

ENTRYPOINT ["/pm-controller-github"]

# Runtime stage - Greenhouse Teams provider
FROM gcr.io/distroless/static:nonroot AS greenhouse-teams

WORKDIR /
COPY --from=builder /workspace/pm-controller-greenhouse-teams .
USER 65532:65532

ENTRYPOINT ["/pm-controller-greenhouse-teams"]
