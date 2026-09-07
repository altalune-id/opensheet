# syntax=docker/dockerfile:1.10

ARG GO_BASE=golang:1.26-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468
ARG STATIC_BASE=gcr.io/distroless/static:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7

FROM ${GO_BASE} AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w \
            -X altalune.id/template/version.Version=${VERSION} \
            -X altalune.id/template/version.Commit=${COMMIT} \
            -X altalune.id/template/version.BuildTime=${BUILD_TIME}" \
        -o /out/altempl ./cmd/altempl

FROM ${STATIC_BASE} AS runtime

COPY --from=build /out/altempl /altempl

USER nonroot:nonroot
EXPOSE 5150

ENTRYPOINT ["/altempl"]
CMD ["serve"]
