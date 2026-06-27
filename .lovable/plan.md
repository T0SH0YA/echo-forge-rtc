
# WebRTC próprio — só a tecnologia, sem portal

Foco: código da stack. Hospedagem (VM, IP, portas) é commodity — terceirizamos pra AWS/Hetzner. O que importa é que **sinalização, STUN, TURN, SFU e SDK são código nosso**.

## Stack que vamos escrever

- **SDK cliente** (`sdk/`) — TypeScript, embrulha `RTCPeerConnection` do browser. API própria: `connect`, `publish`, `subscribe`, `room.on(...)`. Sem dependência de SaaS.
- **Sinalização** (`server/signaling/`) — Go + WebSocket, protocolo JSON nosso (`join/offer/answer/ice/leave`), emite credenciais TURN efêmeras.
- **STUN** (`server/stun/`) — Go, do zero, RFC 5389. ~600 LOC.
- **TURN** (`server/turn/`) — Go, do zero, RFC 5766/8656. Relay UDP/TCP/TLS, allocations, channels, permissions.
- **SFU** (`server/sfu/`) — Go. Stack ICE/DTLS/SRTP construído camada por camada. Forwarding RTP, simulcast, RTCP feedback (NACK/PLI/REMB/transport-cc).
- **Playground** (`playground/`) — página HTML mínima neste projeto Lovable só pra testar o SDK contra os servidores enquanto desenvolvemos. Não é landing, é bancada de teste.

Tudo vive **neste repositório**, em pastas separadas. Lovable é nosso editor + git + preview do playground. Os binários Go você roda numa VM (eu te dou Dockerfile + comando).

## Estrutura de pastas

```text
/sdk                    # TypeScript, publica no npm
/server/signaling       # Go
/server/stun            # Go
/server/turn            # Go
/server/sfu             # Go
/server/common          # tipos/utils Go compartilhados
/infra                  # Dockerfiles, docker-compose, deploy.sh
/playground             # página de teste (rota / do Lovable)
/docs/protocol          # specs internas (formato de mensagens, etc.)
```

## Etapas (sem prazos, cada uma testável)

### Etapa 1 — Esqueleto + protocolo
- Criar estrutura de pastas acima
- Definir e documentar o **protocolo de sinalização** (mensagens JSON, ordem, erros) em `docs/protocol/signaling.md`
- Definir API pública do SDK em `docs/protocol/sdk.md`
- Setup Go workspace (`go.work`) com módulos para cada server
- Setup SDK TS com build (tsup) pronto pra `npm publish`

### Etapa 2 — Sinalização + SDK + sala P2P ✅
- [x] Servidor `signaling` Go (gorilla/websocket): salas em memória, broadcast SDP/ICE, ping/pong, hello-timeout, room GC
- [x] SDK com perfect negotiation, mesh P2P, publishCamera/Screen, eventos
- [x] Transporte `bc://` (BroadcastChannel) pra testar duas abas sem servidor
- [x] Playground real (conectar, publicar, ver remotos)
- [ ] STUN/TURN: ainda nenhum (LAN/loopback funciona; pra cross-NAT precisa Etapa 3/4)

### Etapa 3 — STUN próprio
- Implementar RFC 5389 em Go puro (header, atributos, MESSAGE-INTEGRITY HMAC-SHA1, FINGERPRINT CRC-32, XOR-MAPPED-ADDRESS)
- Testes unitários contra vetores da RFC
- Teste de integração: SDK descobre seu IP público via nosso STUN
- Suporte IPv4+IPv6, multiplos endereços

### Etapa 4 — TURN próprio
- RFC 5766/8656: Allocate, Refresh, CreatePermission, ChannelBind, Send/Data, ChannelData
- Long-term credentials + HMAC time-limited (compatível com padrão coturn)
- Relay UDP primeiro; depois TCP e TLS (5349)
- Sinalização passa a emitir credenciais TURN; SDK passa a usar
- Teste: simular NAT simétrico, conexão sobe via TURN

### Etapa 5 — SFU: ICE no servidor
- ICE-lite no SFU (servidor não faz checks ativos, só responde STUN binding)
- Candidatos host públicos do servidor anunciados no SDP
- Negociação SDP com offer do cliente, answer do servidor
- Conectividade estabelecida até DTLS

### Etapa 6 — SFU: DTLS handshake
- DTLS 1.2 server (podemos usar `pion/dtls` como base interna ou crypto/tls adaptado — código nosso, dependência mínima MIT)
- Certificado self-signed por sessão, fingerprint no SDP (a=fingerprint)
- Verificação de fingerprint cruzada
- Extração de keying material (RFC 5705) para SRTP

### Etapa 7 — SFU: SRTP + forwarding básico
- SRTP AES-128-GCM (RFC 7714): cifra/decifra, replay window
- Parser RTP/RTCP
- Forwarding 1→N de uma track de vídeo + áudio sem inspeção
- Playground vira sala multipartícipe básica

### Etapa 8 — SFU: feedback e qualidade
- RTCP: NACK + RTX (retransmissão), PLI/FIR (keyframe request), receiver reports
- Transport-CC: feedback de chegada de pacotes
- REMB inicial (estimativa simples), depois GCC (loss + delay based)
- Jitter buffer mínimo no servidor

### Etapa 9 — SFU: simulcast e SVC
- Receber 3 camadas simulcast do publisher (Chrome envia nativamente)
- Layer selection por subscriber baseado em bandwidth estimation
- Switch entre camadas em keyframe
- Suporte VP8 + H264; depois VP9/AV1 com SVC

### Etapa 10 — DataChannel
- SCTP sobre DTLS (RFC 8831)
- Mensagens binárias e texto, ordered/unordered
- API no SDK: `room.sendData(peerId, payload)`

### Etapa 11 — Endurecimento
- Reconexão automática (ICE restart) no SDK
- Métricas Prometheus em todos os servers
- Load test (artillery + headless chrome) — 100, 500, 1000 peers por SFU
- TURN/SFU em múltiplas regiões + roteamento

### Etapa 12 — Extras vendáveis (quando o core estiver sólido)
- Gravação server-side (mux RTP→ffmpeg)
- RTMP egress
- E2EE via Insertable Streams (passthrough no SFU)
- SDKs Swift/Kotlin

## O que faço no próximo turno (Etapa 1)

1. Criar pastas: `/sdk`, `/server/{signaling,stun,turn,sfu,common}`, `/infra`, `/playground`, `/docs/protocol`
2. `go.work` + `go.mod` de cada módulo Go (stubs `main.go` que só logam "ok")
3. SDK TS com `package.json`, `tsconfig`, `tsup`, esqueleto de classes `Client`, `Room`, `LocalTrack`, `RemoteTrack`
4. `docs/protocol/signaling.md` — especificação completa do protocolo de mensagens
5. `docs/protocol/sdk.md` — API pública do SDK
6. `/playground` — página simples no Lovable com input de URL/token e dois `<video>` (placeholder, sem lógica ainda)
7. `infra/README.md` com instrução de `docker compose up` local

Sem cloud, sem auth, sem billing, sem landing. Só código.

## Confirmação pra eu começar

Só me responde **"vai"** e eu executo a Etapa 1. Não preciso de nome de produto, domínio nem cloud agora — isso só importa muito mais tarde.
