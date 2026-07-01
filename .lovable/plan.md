## Recomendação: iframe embed com postMessage, não API REST

Pra videoconferência em MFE, **iframe é o padrão da indústria** (Zoom, Whereby, Daily, Jitsi todos fazem assim). Motivo técnico: getUserMedia, WebRTC, DTLS e permissões de câmera/mic são acoplados ao *browsing context*. Se você tentar expor só via API REST, a Teli teria que reimplementar toda a camada de mídia — perde o ponto de ser MFE.

API REST entra como **complemento**, não substituto: usada pelo backend da Teli pra emitir tokens de sala (server-to-server), nunca pelo front.

## Arquitetura proposta

```text
┌─────────────── teli.app.br (Lovable) ───────────────┐
│                                                     │
│   <TeliMeeting roomId="x" token="jwt" />            │
│         │                                           │
│         ▼                                           │
│   <iframe src="echo-forge-rtc.lovable.app/embed     │
│                ?room=x&token=jwt"                   │
│           allow="camera; microphone; display-capture│
│                  ; autoplay" />                     │
│         ▲    ▲                                      │
│         │    │ postMessage (eventos + comandos)     │
│         │    │                                      │
└─────────┼────┼──────────────────────────────────────┘
          │    │
┌─────────▼────▼─── echo-forge-rtc (este app) ────────┐
│  /embed  → sala renderizada, sem lobby, sem header  │
│  /       → app standalone (continua funcionando)    │
│  /api/token → REST: backend Teli troca api-key      │
│               por JWT de sala (server-to-server)    │
│                                                     │
│  Signaling: wss://sig.teli.app.br (já no ar)        │
└─────────────────────────────────────────────────────┘
```

Cada app deploya no seu próprio ciclo. Zero dependência de código. Contrato = URL + postMessage + REST de token.

## O que a plano constrói neste app

### 1. Rota `/embed` (nova)
Versão "sem lobby" do `src/routes/index.tsx`: lê `room`, `token`, `name`, `theme` da querystring, entra direto na sala, UI compacta pra caber em iframe. O `/` continua igual pra uso standalone.

### 2. Bridge postMessage (`src/lib/embed-bridge.ts`)
Contrato tipado dos dois lados:

- **Eventos emitidos pra parent** (Teli escuta): `ready`, `joined`, `peer-joined`, `peer-left`, `left`, `error`, `permission-denied`, `device-changed`.
- **Comandos recebidos do parent** (Teli chama): `mute`, `unmute`, `camera-off`, `camera-on`, `leave`, `switch-device`.
- Validação de `event.origin` contra allowlist (`https://*.teli.app.br`, `http://localhost:*` em dev).

### 3. Rota `POST /api/token` (server route)
Endpoint público server-to-server: recebe `{ apiKey, roomId, userId, ttl }`, valida `apiKey` (secret `TELI_API_KEY`), retorna JWT curto assinado (secret `SIGNALING_JWT_SECRET`). Backend da Teli chama isso; front nunca vê a apiKey. O signaling Go (já rodando em `sig.teli.app.br`) passa a validar esse JWT — pequena mudança em `server/signaling/main.go` numa fase seguinte, opcional agora.

### 4. Snippet de integração pra Teli (`docs/embed.md`)
Componente React pronto pra colar no app da Teli:

```tsx
<iframe
  src={`https://echo-forge-rtc.lovable.app/embed?room=${roomId}&token=${token}`}
  allow="camera; microphone; display-capture; autoplay"
  className="w-full h-full border-0"
/>
```

Mais o hook `useTeliMeeting()` que faz o postMessage handshake.

### 5. Headers de embed
Ajustar resposta pra permitir iframe só de origens Teli: `Content-Security-Policy: frame-ancestors https://*.teli.app.br https://teli.app.br http://localhost:*`. Bloqueia clickjacking de qualquer outro site.

## Por que NÃO só API REST

- WebRTC precisa rodar no browser do usuário final; REST não transporta mídia.
- getUserMedia exige `allow="camera; microphone"` que só o *frame* que renderiza a UI recebe.
- Reconexão, ICE restart, jitter buffer, seleção de camada — tudo state client-side que a Teli teria que reimplementar.

REST fica só pra: emitir token, listar salas ativas, encerrar sala remotamente, buscar gravação — coisas que o *backend* da Teli precisa, não o front.

## Por que NÃO Web Component / NPM package

- **Web Component**: compartilha DOM/CSS com a Teli → conflito de estilos, e ainda assim precisa dos mesmos permissions do iframe. Só faz sentido se você quer que a Teli controle o layout dos tiles de vídeo pixel a pixel.
- **NPM package**: acopla os ciclos de build. Toda mudança neste app obriga a Teli a fazer `bun add`, rebuild, redeploy. Perde a independência que você pediu.

## Segurança

- CORS estrito no `/api/token` (só origens Teli).
- `frame-ancestors` estrito no `/embed`.
- Token JWT com TTL curto (ex: 2h) e `roomId` embutido — mesmo vazando, só serve pra uma sala.
- `apiKey` da Teli nunca sai do backend dela.

## Fora de escopo neste plano

- Mudança no signaling Go pra validar JWT (posso fazer depois; hoje ele aceita qualquer `token`).
- SDK npm (podemos publicar mais tarde como *alternativa* ao iframe, sem remover o iframe).
- UI do lado da Teli (fica pro outro projeto Lovable).