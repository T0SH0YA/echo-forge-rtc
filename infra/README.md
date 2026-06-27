# Infra

## Local (todos os serviços)

```bash
cd infra
docker compose build
docker compose up
```

Endpoints:
- Sinalização: `http://localhost:8080/healthz`
- STUN: `udp://localhost:3478` (stub, só loga)
- SFU: `http://localhost:8081/healthz`
- TURN: desativado até a Etapa 4

## Deploy em VM única (resumo — detalhado por etapa depois)

```bash
# Na VM (Ubuntu 22.04+):
sudo apt update && sudo apt install -y docker.io docker-compose-plugin
git clone <este-repo> && cd <repo>/infra
sudo docker compose up -d
```

Para produção:
- IP público fixo
- Portas: `80/tcp`, `443/tcp` (Caddy), `3478/udp` (STUN/TURN), `5349/tcp` (TURN TLS), `49152-65535/udp` (TURN relay)
- Firewall só fechado para SSH

Caddy + TLS automático entra junto com a Etapa 2.
