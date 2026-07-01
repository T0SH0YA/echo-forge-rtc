# Embed — usar este app como MFE de vídeo na Teli

Este app é um MFE independente. A Teli embeda via `<iframe>` e conversa
com ele por `postMessage`. Zero acoplamento de código, ciclos de deploy
separados.

## 1. Backend Teli emite o token da sala

Toda sala precisa de um JWT curto. O backend da Teli chama este endpoint
server-to-server (nunca do browser):

```bash
curl -X POST https://echo-forge-rtc.lovable.app/api/public/token \
  -H "Content-Type: application/json" \
  -d '{
    "apiKey": "'"$TELI_API_KEY"'",
    "roomId": "meeting-123",
    "userId": "user-abc",
    "ttlSeconds": 7200
  }'
```

Resposta:

```json
{ "token": "eyJhbGciOi...", "expiresAt": 1730000000 }
```

- `TELI_API_KEY` já está provisionada neste projeto (segredo). Peça o valor
  em Project → Secrets e configure no backend da Teli.
- Token tem TTL curto e carrega `roomId`, `userId` — se vazar, só serve
  pra uma sala.

## 2. Front da Teli renderiza o iframe

```tsx
// No app da Teli (Lovable)
export function TeliMeeting({ roomId, token, userName }: {
  roomId: string;
  token: string;
  userName?: string;
}) {
  const src = new URL("https://echo-forge-rtc.lovable.app/embed");
  src.searchParams.set("room", roomId);
  src.searchParams.set("token", token);
  if (userName) src.searchParams.set("name", userName);
  src.searchParams.set("theme", "dark");

  return (
    <iframe
      src={src.toString()}
      allow="camera; microphone; display-capture; autoplay"
      className="w-full h-full border-0"
      title="Teli Meeting"
    />
  );
}
```

`allow="camera; microphone"` é obrigatório — sem ele o browser bloqueia
`getUserMedia` dentro do iframe.

## 3. Comunicação bidirecional (postMessage)

### Eventos emitidos pelo iframe (Teli escuta)

| `t`                | payload                                | quando                          |
| ------------------ | -------------------------------------- | ------------------------------- |
| `ready`            | —                                      | iframe carregou                 |
| `joined`           | `{ peerId, room }`                     | entrou na sala                  |
| `peer-joined`      | `{ peerId }`                           | outro participante entrou       |
| `peer-left`        | `{ peerId }`                           | outro participante saiu         |
| `left`             | —                                      | usuário local saiu              |
| `error`            | `{ message }`                          | erro fatal                      |
| `permission-denied`| `{ kind: "camera"\|"microphone"\|"both" }` | negou permissão            |
| `device-changed`   | `{ kind: "audio"\|"video", enabled }` | mic/câmera on/off               |

```tsx
useEffect(() => {
  const onMsg = (e: MessageEvent) => {
    if (e.origin !== "https://echo-forge-rtc.lovable.app") return;
    const msg = e.data;
    if (msg?.t === "joined") console.log("entrou na sala", msg.room);
    if (msg?.t === "peer-joined") toast(`${msg.peerId} entrou`);
    if (msg?.t === "left") navigate("/dashboard");
  };
  window.addEventListener("message", onMsg);
  return () => window.removeEventListener("message", onMsg);
}, []);
```

### Comandos do parent pro iframe (Teli manda)

| `t`             | payload                                   |
| --------------- | ----------------------------------------- |
| `mute`          | —                                         |
| `unmute`        | —                                         |
| `camera-off`    | —                                         |
| `camera-on`     | —                                         |
| `leave`         | —                                         |
| `switch-device` | `{ kind: "audio"\|"video", deviceId }`    |

```tsx
function sendCmd(iframe: HTMLIFrameElement, cmd: object) {
  iframe.contentWindow?.postMessage(cmd, "https://echo-forge-rtc.lovable.app");
}

// Ex: botão "sair" no header da Teli
sendCmd(iframeRef.current!, { t: "leave" });
```

## 4. Independência

- Este app roda standalone em `/` (lobby próprio pra testes).
- A rota `/embed` é a superfície MFE.
- Sinalização em `wss://sig.teli.app.br` — compartilhada, mas o contrato
  público é só o URL do iframe + postMessage + `/api/public/token`.
- Deploy dos dois lados é independente. Mudanças aqui não quebram a Teli
  enquanto o contrato acima não mudar.

## 5. Segurança

- `/api/public/token` valida `TELI_API_KEY` com comparação timing-safe e
  restringe CORS a `*.teli.app.br`.
- JWT: HS256, TTL ≤ 24h, assinado com `SIGNALING_JWT_SECRET`.
- O iframe valida a origem de comandos recebidos (allowlist em
  `src/lib/embed-bridge.ts`).
- TODO: adicionar `Content-Security-Policy: frame-ancestors https://*.teli.app.br`
  na resposta do `/embed` pra bloquear clickjacking de origens não-Teli.
