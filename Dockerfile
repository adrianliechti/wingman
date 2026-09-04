# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /server /src


FROM alpine

RUN apk add --no-cache tini ca-certificates mailcap

COPY --from=build /server /

EXPOSE 8080

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/server"]
