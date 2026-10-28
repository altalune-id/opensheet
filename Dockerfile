# syntax=docker/dockerfile:1.10

ARG GO_BASE=golang:1.26.10-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308
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
            -X altalune.id/opensheet/version.Version=${VERSION} \
            -X altalune.id/opensheet/version.Commit=${COMMIT} \
            -X altalune.id/opensheet/version.BuildTime=${BUILD_TIME}" \
        -o /out/opensheet ./cmd/opensheet

FROM ${STATIC_BASE} AS runtime

COPY --from=build /out/opensheet /opensheet

USER nonroot:nonroot
EXPOSE 5150

ENTRYPOINT ["/opensheet"]
CMD ["serve"]
