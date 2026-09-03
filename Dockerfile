ARG GO_VERSION=1.27.0
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src
RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w \
        -X github.com/zijiren233/route-controller/internal/version.Version=${VERSION} \
        -X github.com/zijiren233/route-controller/internal/version.GitCommit=${GIT_COMMIT} \
        -X github.com/zijiren233/route-controller/internal/version.BuildDate=${BUILD_DATE}" \
      -o /out/route-controller ./cmd/route-controller

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/route-controller /usr/local/bin/route-controller
ENTRYPOINT ["/usr/local/bin/route-controller"]
CMD ["run"]
