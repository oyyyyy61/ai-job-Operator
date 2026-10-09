# Build the controller binary.
FROM golang:1.26 AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG GOPROXY=https://proxy.golang.org,direct

WORKDIR /workspace
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY api/ api/
COPY controllers/ controllers/
COPY webhooks/ webhooks/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o manager main.go

# Run as an unprivileged user in a minimal image.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
