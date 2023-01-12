# Build the manager binary
FROM quay.io/centos/centos:stream9 AS builder
RUN dnf install -y golang

# Ensure correct Go version
ENV GO_VERSION=1.18
RUN go install golang.org/dl/go${GO_VERSION}@latest
RUN ~/go/bin/go${GO_VERSION} download
RUN /bin/cp -f ~/go/bin/go${GO_VERSION} /usr/bin/go
RUN go version

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download


# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager main.go


FROM registry.access.redhat.com/ubi9/ubi-minimal:9.1.0

WORKDIR /
COPY --from=builder /workspace/manager .

# Add Fence Agents
RUN microdnf install -y yum-utils
RUN yum-config-manager --set-enabled rhel-9-for-x86_64-highavailability-rpms
RUN microdnf install -y fence-agents-all-4.10.0

# COPY --from=builder /etc/yum.repos.d/centos-addons.repo .
# RUN dnf -y --enablerepo=highavailability install fence-agents-all 

ENTRYPOINT ["/manager"]
