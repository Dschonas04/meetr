FROM golang:1.27.4-alpine AS bau
WORKDIR /quelle
COPY go.mod ./
RUN go mod download
COPY . .
ARG VERSION=entwicklung
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /meetr .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 meetr
ENV TZ=Europe/Berlin
USER meetr
COPY --from=bau /meetr /usr/local/bin/meetr
EXPOSE 8370
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8370/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/meetr"]
