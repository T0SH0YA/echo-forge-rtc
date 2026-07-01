# Embed — usar este app como MFE de vídeo na Teli

Este app é um MFE independente. A Teli embeda via `<iframe>` e conversa
com ele por `postMessage`. Zero acoplamento de código, ciclos de deploy
separados.

- **URL publicada deste MFE:** `https://echo-forge-rtc.lovable.app`
- **Endpoint de token:** `POST https://echo-forge-rtc.lovable.app/api/public/token`
- **Rota de embed:** `https://echo-forge-rtc.lovable.app/embed`
- **Signaling:** `wss://sig.teli.app.br`

## 1. Backend Teli emite o token da sala

Toda sala precisa de um JWT curto. O backend da Teli chama este endpoint
server-to-server (nunca do browser):

```bash
curl -X POST https://echo-forge-rtc.lovable.app/api/public/token \
  -H "Content-Type: application/json" \
  -d '{
    "apiKey": "'"$TELI_RTC_API_KEY"'",
    "roomId": "meeting-123",
    "userId": "user-abc",
    "ttlSeconds": 7200
  }'
```

Resposta:

```json
{ "token": "eyJhbGciOi...", "expiresAt": 1730000000 }
```

Token tem TTL curto e carrega `roomId`, `userId` — se vazar, só serve
pra uma sala.

## 2. Configuração dos secrets

### Neste projeto (echo-forge-rtc)

| Secret                  | Uso                                                     |
| ----------------------- | ------------------------------------------------------- |
| `TELI_API_KEY`          | Autentica requisições vindas do backend da Teli.        |
| `SIGNALING_JWT_SECRET`  | Assina o JWT de sala (HS256). Mesmo secret no Go signaling. |

### No projeto Teli — atenção pra NÃO trocar os valores

| Secret (na Teli)    | Valor exato                                             |
| ------------------- | ------------------------------------------------------- |
| `TELI_RTC_TOKEN_URL`| `https://echo-forge-rtc.lovable.app/api/public/token`   |
| `TELI_RTC_API_KEY`  | O mesmo valor de `TELI_API_KEY` deste projeto           |

> **Secrets no Lovable são write-only.** Depois de salvos você não consegue
> lê-los de novo. Se perdeu o valor da `TELI_API_KEY`, gere um novo aqui e
> atualize `TELI_RTC_API_KEY` na Teli — os dois têm que bater exatamente.

## 3. Front da Teli renderiza o iframe

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

## 4. Comunicação bidirecional (postMessage)

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

## 5. Smoke test (rode depois de configurar os secrets)

Substitua `<TELI_RTC_API_KEY>` pelo valor real (server-side, nunca no browser):

```bash
curl -i -X POST https://echo-forge-rtc.lovable.app/api/public/token \
  -H "Content-Type: application/json" \
  -d '{"apiKey":"<TELI_RTC_API_KEY>","roomId":"teste-1","userId":"user-1"}'
```

- `200` + `{"token":"eyJ...","expiresAt":...}` → tudo certo, é só o front da Teli montar o iframe com esse token.
- `401 unauthorized` → a `TELI_RTC_API_KEY` da Teli não bate com `TELI_API_KEY` daqui.
- `400 invalid roomId/userId` → id fora do regex permitido (ver Troubleshooting).
- `500 server not configured` → falta `TELI_API_KEY` ou `SIGNALING_JWT_SECRET` neste projeto.

## 6. Troubleshooting

### `TypeError: Invalid URL: 'j4CptPSq6R...'` no edge function da Teli
Os secrets foram **trocados** na hora de salvar: o valor da API key foi salvo
em `TELI_RTC_TOKEN_URL`, então o `fetch()` tenta bater numa URL que não é URL.
Corrija conforme a tabela na seção 2 — URL vai no `_URL`, chave vai no `_API_KEY`.

### `401 unauthorized` do `/api/public/token`
`TELI_RTC_API_KEY` (na Teli) ≠ `TELI_API_KEY` (aqui). Como secrets são
write-only, o jeito seguro é: gerar novo valor aqui, atualizar lá, salvar
os dois com o mesmo valor.

### `400 invalid roomId` ou `400 invalid userId`
- `roomId` precisa casar `^[a-zA-Z0-9_-]{1,128}$`
- `userId` precisa casar `^[a-zA-Z0-9_.-]{1,128}$`

Sem espaço, sem acento, sem `@`, sem `/`. Se o id da Teli tem esses
caracteres, hash/slugify antes de mandar.

### Iframe carrega mas o browser não pede câmera/microfone
Faltou `allow="camera; microphone"` no `<iframe>`. Sem essa policy o
`getUserMedia` é bloqueado silenciosamente dentro do iframe.

### `postMessage` do parent chega no iframe mas é ignorado
A origem do parent não está na allowlist de `src/lib/embed-bridge.ts`
(`DEFAULT_ALLOWED_ORIGINS`). Por padrão liberamos `*.teli.app.br`,
`*.lovable.app` e `localhost`. Domínio custom novo precisa entrar lá.

### Eventos do iframe não chegam no parent
Confira que o listener no parent filtra por
`e.origin === "https://echo-forge-rtc.lovable.app"` — se estiver comparando
com outra URL (ex: preview vs. published), os eventos são descartados.

## 7. Independência

- Este app roda standalone em `/` (lobby próprio pra testes).
- A rota `/embed` é a superfície MFE.
- Sinalização em `wss://sig.teli.app.br` — compartilhada, mas o contrato
  público é só o URL do iframe + postMessage + `/api/public/token`.
- Deploy dos dois lados é independente. Mudanças aqui não quebram a Teli
  enquanto o contrato acima não mudar.

## 8. Segurança

- `/api/public/token` valida `TELI_API_KEY` com comparação timing-safe e
  restringe CORS a `*.teli.app.br`.
- JWT: HS256, TTL ≤ 24h, assinado com `SIGNALING_JWT_SECRET`.
- O iframe valida a origem de comandos recebidos (allowlist em
  `src/lib/embed-bridge.ts`).
- TODO: adicionar `Content-Security-Policy: frame-ancestors https://*.teli.app.br`
  na resposta do `/embed` pra bloquear clickjacking de origens não-Teli.
