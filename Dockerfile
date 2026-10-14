FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /deodes-api ./cmd/api-server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /deodes-api /usr/bin/deodes-api
COPY config/ /etc/deodes/
COPY internal/database/migrations/ /etc/deodes/migrations/
EXPOSE 1100
ENTRYPOINT ["/usr/bin/deodes-api"]
