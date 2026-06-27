# Multi-service Go builder. Define SERVICE via build arg.
# Uso: docker build --build-arg SERVICE=signaling -f infra/Dockerfile.go server/
ARG SERVICE

FROM golang:1.22-alpine AS builder
ARG SERVICE
WORKDIR /src
COPY . .
# go.work cuida do multi-module; baixa deps de todos os módulos.
RUN cd ${SERVICE} && go mod tidy && go build -trimpath -ldflags="-s -w" -o /out/app .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]
