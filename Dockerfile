# Build the manager binary
# Override BASE_IMAGE to build from another registry, e.g. docker.io/library/golang:1.26
ARG BASE_IMAGE=golang:1.26

# The scanner CLIs, in their own stage: it doesn't depend on the source, so a code change never
# downloads them again, and BuildKit fetches them while the operator compiles.
FROM ${BASE_IMAGE} AS tools
ARG TARGETARCH
WORKDIR /tools

# Fetch the pinned xeol release and verify it against the release checksums.
# Bumping XEOL_VERSION is deliberate: re-check AGENTS.md "xeol CLI facts" and refresh testdata.
ARG XEOL_VERSION=0.10.8
RUN curl -sSfL --retry 3 --retry-all-errors --speed-limit 10000 --speed-time 30 -o /tmp/xeol.tar.gz \
      "https://github.com/xeol-io/xeol/releases/download/v${XEOL_VERSION}/xeol_${XEOL_VERSION}_linux_${TARGETARCH:-amd64}.tar.gz" && \
    curl -sSfL --retry 3 --retry-all-errors --speed-limit 10000 --speed-time 30 -o /tmp/checksums.txt \
      "https://github.com/xeol-io/xeol/releases/download/v${XEOL_VERSION}/xeol_${XEOL_VERSION}_checksums.txt" && \
    cd /tmp && grep "xeol_${XEOL_VERSION}_linux_${TARGETARCH:-amd64}.tar.gz" checksums.txt | sed 's#  .*#  xeol.tar.gz#' | sha256sum -c - && \
    tar -xzf xeol.tar.gz xeol && mv xeol /tools/xeol

# syft catalogs images into an SBOM that xeol then matches. It is pinned separately from xeol,
# whose built-in syft (v1.10) needs over twice the memory. Bump it on its own; re-run the scanner tests.
ARG SYFT_VERSION=1.55.0
RUN curl -sSfL --retry 3 --retry-all-errors --speed-limit 10000 --speed-time 30 -o /tmp/syft.tar.gz \
      "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/syft_${SYFT_VERSION}_linux_${TARGETARCH:-amd64}.tar.gz" && \
    curl -sSfL --retry 3 --retry-all-errors --speed-limit 10000 --speed-time 30 -o /tmp/syft_checksums.txt \
      "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/syft_${SYFT_VERSION}_checksums.txt" && \
    cd /tmp && grep "syft_${SYFT_VERSION}_linux_${TARGETARCH:-amd64}.tar.gz" syft_checksums.txt | sed 's#  .*#  syft.tar.gz#' | sha256sum -c - && \
    tar -xzf syft.tar.gz syft && mv syft /tools/syft

# trivy matches the CycloneDX SBOM from the same syft run against its vulnerability DB (--trivy).
# Bumping TRIVY_VERSION is deliberate: re-check the CycloneDX fix-up property names
# (internal/scanner/trivy.go) and refresh the trivy testdata.
ARG TRIVY_VERSION=0.76.0
RUN case "${TARGETARCH:-amd64}" in amd64) TA=64bit ;; arm64) TA=ARM64 ;; *) echo "unsupported arch ${TARGETARCH}" >&2; exit 1 ;; esac && \
    curl -sSfL --retry 3 --retry-all-errors --speed-limit 10000 --speed-time 30 -o /tmp/trivy.tar.gz \
      "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-${TA}.tar.gz" && \
    curl -sSfL --retry 3 --retry-all-errors --speed-limit 10000 --speed-time 30 -o /tmp/trivy_checksums.txt \
      "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_checksums.txt" && \
    cd /tmp && grep " trivy_${TRIVY_VERSION}_Linux-${TA}.tar.gz$" trivy_checksums.txt | sed 's#  .*#  trivy.tar.gz#' | sha256sum -c - && \
    tar -xzf trivy.tar.gz trivy && mv trivy /tools/trivy

FROM ${BASE_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build
# the GOARCH has no default value to allow the binary to be built according to the host where the command
# was called. For example, if we call task docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
# The module and build caches persist between builds, so a code change recompiles only what changed.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o manager ./cmd

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=tools /tools/xeol /usr/local/bin/xeol
COPY --from=tools /tools/syft /usr/local/bin/syft
COPY --from=tools /tools/trivy /usr/local/bin/trivy
USER 65532:65532

ENTRYPOINT ["/manager"]
