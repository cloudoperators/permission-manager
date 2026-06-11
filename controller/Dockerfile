# Build stage
FROM golang:1.22 AS builder

ARG TARGET

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

# Build only the requested binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -o /out/pm-controller ./cmd/${TARGET}/

# Runtime stage - Kubernetes provider
FROM gcr.io/distroless/static:nonroot AS kubernetes

WORKDIR /
COPY --from=builder /out/pm-controller .
USER 65532:65532

ENTRYPOINT ["/pm-controller"]

# Runtime stage - Aggregator
FROM gcr.io/distroless/static:nonroot AS aggregator

WORKDIR /
COPY --from=builder /out/pm-controller .
USER 65532:65532

ENTRYPOINT ["/pm-controller"]

# Runtime stage - GitHub provider
FROM gcr.io/distroless/static:nonroot AS github

WORKDIR /
COPY --from=builder /out/pm-controller .
USER 65532:65532

ENTRYPOINT ["/pm-controller"]

# Runtime stage - Greenhouse Teams provider
FROM gcr.io/distroless/static:nonroot AS greenhouse-teams

WORKDIR /
COPY --from=builder /out/pm-controller .
USER 65532:65532

ENTRYPOINT ["/pm-controller"]
