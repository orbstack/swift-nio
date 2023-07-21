# syntax=docker/dockerfile:1

FROM alpine:20230329 AS base
FROM golang:1-alpine AS go-base

FROM go-base as build
COPY scon /build/scon
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg \
<<EOT sh -eux
mkdir /out
cd /build/scon
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /out/ocitar ./cmd/ocitar
EOT

FROM base AS final
RUN apk add --no-cache tar bash
COPY --from=build /out/ocitar /usr/local/bin/ocitar

# flatten
FROM scratch AS flat
COPY --from=final / /
