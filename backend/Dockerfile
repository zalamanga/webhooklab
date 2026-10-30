FROM golang:1.27.3-alpine AS build
WORKDIR /build
ENV CGO_ENABLED=0 GOMAXPROCS=2 GOMEMLIMIT=384MiB GOGC=50 GOFLAGS=-p=2
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && go build -trimpath -ldflags="-s -w" -o /out/receiver ./cmd/receiver

FROM alpine:3.23
RUN addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app
WORKDIR /app
COPY --from=build --chown=10001:10001 /out/ /app/
USER 10001:10001
EXPOSE 8080 8082
CMD ["/app/api"]
