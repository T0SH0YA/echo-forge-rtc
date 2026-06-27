# WebRTC próprio

Stack WebRTC end-to-end: sinalização, STUN, TURN, SFU e SDK — tudo código nosso.

## Estrutura

```
/sdk                # SDK TypeScript (npm)
/server/signaling   # WebSocket de sinalização (Go)
/server/stun        # STUN próprio (Go, RFC 5389)
/server/turn        # TURN próprio (Go, RFC 5766/8656)
/server/sfu         # SFU próprio (Go, ICE/DTLS/SRTP)
/server/common      # tipos compartilhados
/infra              # Dockerfiles, docker-compose, deploy
/playground         # bancada de teste do SDK (Lovable)
/docs/protocol      # especificações internas
```

## Status

Etapa 1 — Esqueleto + protocolo: **completa**.

Próxima: Etapa 2 — sinalização funcional + SDK P2P + playground real.

Veja `.lovable/plan.md` para o roadmap completo.
