# Build go
FROM golang:latest AS builder
WORKDIR /app
COPY . .
ENV CGO_ENABLED=0
RUN go mod download
RUN go build -v -o Aiko-Hysteria -trimpath -ldflags "-s -w" ./cmd/server

# Release
FROM  alpine
RUN  apk --update --no-cache add tzdata ca-certificates \
    && cp /usr/share/zoneinfo/Asia/Ho_Chi_Minh /etc/localtime

COPY --from=builder /app/Aiko-Hysteria /usr/local/bin
ENTRYPOINT Aiko-Hysteria -api="$API" -token="$TOKEN" -node="$NODE"
