# Build the manager binary
FROM quay.io/konveyor/builder:ubi9-latest AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.sum ./

# Set GOTOOLCHAIN to auto to allow Go to download newer versions
# Set to local to avoid downloading newer versions of Go
ENV GOTOOLCHAIN=auto

# Copy the go source
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
COPY hack/ hack/
COPY pkg/ pkg/
COPY version/ version/
COPY vendor/ vendor/

COPY .git/ .git/

RUN go version

RUN git config --global --add safe.directory /workspace
RUN ./hack/build.sh

FROM quay.io/centos/centos:stream9

WORKDIR /
COPY --from=builder /workspace/manager .

# Add many Fence Agents packages
RUN dnf install -y dnf-plugins-core \
    && dnf --enablerepo=highavailability install -y fence-agents-amt-ws fence-agents-apc-snmp fence-agents-cisco-ucs \
    fence-agents-eaton-snmp fence-agents-emerson fence-agents-eps fence-agents-ibmblade fence-agents-ifmib fence-agents-ilo2 \
    fence-agents-intelmodular fence-agents-ipdu fence-agents-ipmilan fence-agents-redfish fence-agents-rhevm \
    fence-agents-vmware-rest fence-agents-vmware-soap \
    fence-agents-kubevirt fence-agents-ibm-powervs fence-agents-ibm-vpc \
    fence-agents-aws fence-agents-azure-arm fence-agents-gce \
    && dnf clean all -y

USER 65532:65532
ENTRYPOINT ["/manager"]
