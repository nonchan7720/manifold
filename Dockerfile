# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS builder

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

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
ENV TZ=Asia/Tokyo \
  SKIP_SECURE_CLIENT=true

# Ownership verification marker for the MCP Registry (must match server.json name)
LABEL io.modelcontextprotocol.server.name="io.github.nonchan7720/manifold"

COPY --from=builder --chown=nonroot:nonroot /bin/manifold /usr/local/bin/manifold
# current directory is `/home/nonroot`
# COPY --chown=nonroot:nonroot config.yaml config.yaml
USER nonroot:nonroot

CMD ["manifold", "gateway"]
