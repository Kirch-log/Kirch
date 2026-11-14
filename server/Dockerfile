# kirch-server image. Build from the repo root:
#   docker build -f server/Dockerfile -t kirch-server .
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kirch-server ./server

FROM alpine:3.20
RUN adduser -D -u 10001 kirch && mkdir /data && chown kirch /data
COPY --from=build /out/kirch-server /usr/local/bin/kirch-server
USER kirch
ENV KIRCH_DATA_DIR=/data KIRCH_TCP_ADDR=:9418 KIRCH_HTTP_ADDR=:8080
VOLUME /data
EXPOSE 9418 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["kirch-server"]
