FROM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/atlas-api ./cmd/api

FROM alpine:3.20

RUN adduser -D -H -s /sbin/nologin appuser
WORKDIR /app

COPY --from=builder /out/atlas-api /app/atlas-api

USER appuser

EXPOSE 8080

CMD ["/app/atlas-api"]
