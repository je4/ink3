FROM golang:1.27 AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/revcatfront ./cmd/revcatfront

FROM alpine AS runner

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /out/revcatfront /app/revcatfront

COPY config/ /config/

RUN mkdir -p /app/cache /app/data

WORKDIR /app

RUN mkdir -p /var/www/vhosts/performance.ausstellung.cc/digitalesee

RUN touch /var/www/vhosts/performance.ausstellung.cc/digitalesee/collage.json

EXPOSE 8445
ENTRYPOINT ["/app/revcatfront", "-config", "/config/revcatfront.toml"]
