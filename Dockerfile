# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
FROM --platform=$BUILDPLATFORM golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452 AS builder

ARG TARGETARCH
ARG TARGETPLATFORM
ARG VERSION=main

ENV GO111MODULE=on \
  GOPATH=/go \
  GOBIN=/go/bin \
  GOARCH=$TARGETARCH

WORKDIR /workspace

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 go build \
  -ldflags="-w -s -extldflags '-static'" \
  -o /bin/manifold \
  ./main.go \
  && chmod +x /bin/manifold

FROM gcr.io/distroless/static:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6
ENV TZ=Asia/Tokyo \
  SKIP_SECURE_CLIENT=true

# Ownership verification marker for the MCP Registry (must match server.json name)
LABEL io.modelcontextprotocol.server.name="io.github.nonchan7720/manifold"

COPY --from=builder --chown=nonroot:nonroot /bin/manifold /usr/local/bin/manifold
# current directory is `/home/nonroot`
# COPY --chown=nonroot:nonroot config.yaml config.yaml
USER nonroot:nonroot

CMD ["manifold", "gateway"]
