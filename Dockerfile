# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS builder

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

FROM gcr.io/distroless/static:nonroot@sha256:d62067879de890a1def9da0e7766b7d20810a5e5555cef8506b752437d7a3e88
# SKIP_SECURE_CLIENT=true: in-cluster destinations (ClusterIP etc.) are normal for this image,
# so the private-range (RFC1918 etc.) block is relaxed for operator-configured upstream URLs and
# for tool-caller file URLs. Loopback/link-local (cloud metadata)/multicast stay blocked for
# caller-supplied URLs, CIMD client_id URLs keep the full block, and fileFetch.deniedCIDRs /
# deniedHosts can fence off further internal targets. See README "fileFetch".
ENV TZ=Asia/Tokyo \
  SKIP_SECURE_CLIENT=true

# Ownership verification marker for the MCP Registry (must match server.json name)
LABEL io.modelcontextprotocol.server.name="io.github.nonchan7720/manifold"

COPY --from=builder --chown=nonroot:nonroot /bin/manifold /usr/local/bin/manifold
# current directory is `/home/nonroot`
# COPY --chown=nonroot:nonroot config.yaml config.yaml
USER nonroot:nonroot

CMD ["manifold", "gateway"]
