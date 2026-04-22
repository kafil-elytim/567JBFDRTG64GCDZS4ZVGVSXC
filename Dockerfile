FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o proxy .

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/proxy .
EXPOSE 10000
CMD ["./proxy"]
