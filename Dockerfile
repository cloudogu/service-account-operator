FROM golang:1.26.2 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -mod=mod -a -o service-account-operator cmd/main.go

FROM gcr.io/distroless/static:nonroot
LABEL maintainer="hello@cloudogu.com" \
      NAME="service-account-operator" \
      VERSION="0.0.1"

WORKDIR /
COPY --from=builder /workspace/service-account-operator .
USER nonroot:nonroot

ENTRYPOINT ["/service-account-operator"]
