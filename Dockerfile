# Etapa 1: compilação
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o evaluation-service .

# Etapa 2: imagem final
FROM alpine:3.20

WORKDIR /app

COPY --from=builder /app/evaluation-service .

EXPOSE 8004

CMD ["./evaluation-service"]