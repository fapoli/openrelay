FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o openrelay ./cmd/openrelay

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && adduser -D -H openrelay

WORKDIR /app

COPY --from=builder /app/openrelay ./
COPY projects.yaml ./

USER openrelay

EXPOSE 3000/tcp 7777/udp

CMD ["./openrelay"]
