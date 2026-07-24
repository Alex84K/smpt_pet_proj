FROM golang:alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o mailshield ./cmd/mailshield

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
RUN mkdir -p /app/data /app/keys
COPY --from=builder /app/mailshield .
EXPOSE 2525
CMD ["./mailshield"]
