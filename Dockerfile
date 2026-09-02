# Build stage
FROM golang:1.23-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bot .

# Runtime stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/

# Копируем бинарник
COPY --from=builder /app/bot .

# Директория для persistent volume (SQLite)
RUN mkdir -p /data
ENV DATABASE_PATH=/data/kopeyka.db

EXPOSE 8080

CMD ["./bot"]
