# Deploy — Lightsail + sig.teli.app.br

## 1. Criar instância Lightsail
- AWS Lightsail → **Create instance** → Linux → **Ubuntu 22.04 LTS** → plano $5/mês (1GB RAM mínimo).
- Em **Networking** da instância, anexar **Static IP** e abrir portas:
  - TCP: `22, 80, 443, 8081`
  - UDP: `3478, 7000, 49152-65535` (TURN relay + SFU)

## 2. DNS
No painel do `teli.app.br`, criar record:
```
A    sig    <IP_ESTATICO_LIGHTSAIL>    TTL 300
```
Esperar propagar (`dig sig.teli.app.br +short` → seu IP).

## 3. Provisionar a VM
SSH na instância:
```bash
sudo apt update && sudo apt install -y docker.io docker-compose-plugin git
sudo usermod -aG docker ubuntu && newgrp docker

git clone <URL_DO_SEU_REPO> webrtc && cd webrtc/infra
cp .env.example .env
nano .env   # define ACME_EMAIL e PUBLIC_IP (IP estático do Lightsail)

docker compose up -d --build
docker compose logs -f caddy   # confirma "certificate obtained successfully"
```

## 4. Testar
```bash
curl https://sig.teli.app.br/healthz   # deve responder "ok"
```

## 5. Apontar o SDK pro signaling
No projeto Lovable, criar/editar `.env`:
```
VITE_SIGNALING_URL=wss://sig.teli.app.br
```
Reload do preview. Agora a sala usa o servidor real — funciona entre dispositivos diferentes.

## 6. Atualizar
```bash
cd webrtc && git pull && cd infra && docker compose up -d --build
```
