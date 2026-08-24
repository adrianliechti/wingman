# syntax=docker/dockerfile:1

FROM golang:1-alpine AS build

WORKDIR /src

COPY go.* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /server /src


FROM alpine

RUN apk add --no-cache tini ca-certificates mailcap

COPY --from=build /server /

EXPOSE 8080

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/server"]