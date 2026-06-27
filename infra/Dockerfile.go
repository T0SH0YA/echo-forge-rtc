# Multi-service Go builder. Define SERVICE via build arg.
# Uso: docker build --build-arg SERVICE=signaling -f infra/Dockerfile.go server/
ARG SERVICE

FROM golang:1.22-alpine AS builder
ARG SERVICE
WORKDIR /src
COPY . .
WORKDIR /src/${SERVICE}
RUN go build -trimpath -ldflags="-s -w" -o /out/app .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]
