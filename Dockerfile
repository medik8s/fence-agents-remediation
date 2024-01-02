# Build the manager binary
FROM quay.io/centos/centos:stream8 AS builder
RUN dnf install -y golang git \
    && dnf clean all -y

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Ensure correct Go version
RUN export GO_VERSION=$(grep -E "go [[:digit:]]\.[[:digit:]][[:digit:]]" go.mod | awk '{print $2}') && \
    go install golang.org/dl/go${GO_VERSION}@latest && \
    ~/go/bin/go${GO_VERSION} download && \
    /bin/cp -f ~/go/bin/go${GO_VERSION} /usr/bin/go && \
    go version

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY hack/ hack/
COPY pkg/ pkg/
COPY version/ version/
COPY vendor/ vendor/

# for getting version info
COPY .git/ .git/

# Build
RUN ./hack/build.sh

FROM debian:stable-slim

WORKDIR /
COPY --from=builder /workspace/manager .

# Add Fence Agents and fence-agents-aws packages
RUN apt-get update -y \
    && apt-get install -y ipmitool fence-agents --no-install-recommends \
    && apt-get clean -y \
    && rm -rf /var/lib/apt/lists/*

USER 65532:65532
ENTRYPOINT ["/manager"]
