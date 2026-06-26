FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -ldflags="-s -w" -o payapi-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S payapi \
    && adduser -S -G payapi payapi
WORKDIR /app
COPY --from=builder /app/payapi-server .
RUN chown -R payapi:payapi /app
USER payapi
EXPOSE 3402
CMD ["./payapi-server"]
