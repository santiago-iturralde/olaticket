# Se construye desde la RAÍZ del repo (contexto "."), igual que en Railway:
#   docker build -f backend/Dockerfile .
# El .dockerignore de la raíz deja pasar solo backend/.
FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /api ./cmd/api

FROM alpine:3.22

RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /api ./api

# 8080: API pública. 9091: /metrics para Prometheus (solo red interna).
EXPOSE 8080 9091
ENTRYPOINT ["./api"]
