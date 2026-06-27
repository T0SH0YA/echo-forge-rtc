# Protocolo de Sinalização

Transporte: **WebSocket** (`wss://`), payloads **JSON UTF-8**, uma mensagem por frame.

URL: `wss://<host>/v1/rooms/<roomId>?token=<jwt>`

O `token` é um JWT HS256 com claims `{ room, peer, role, exp }`, emitido por quem opera o serviço (fora do escopo desta spec).

## Envelope

Toda mensagem tem a forma:

```json
{ "t": "<type>", "id": "<uuid?>", "data": { ... } }
```

- `t` — tipo da mensagem (obrigatório)
- `id` — UUID v4 opcional, usado para correlacionar request/response
- `data` — payload específico do tipo

## Ciclo de vida

```text
client                          server
  │── hello ────────────────────►│
  │◄──────────────── welcome ────│   (peerId atribuído, lista de peers)
  │◄──────────────── peer-join ──│   (broadcast quando outros entram)
  │── offer ────────────────────►│
  │◄─────────────────── answer ──│
  │── ice ──────────────────────►│
  │◄────────────────────── ice ──│
  │── leave ────────────────────►│
  │◄──────────────── peer-leave ─│
```

## Mensagens cliente → servidor

### `hello`
Primeira mensagem após abrir o WebSocket. Sem ela o servidor fecha em 5s.

```json
{ "t": "hello", "data": { "sdkVersion": "0.1.0", "capabilities": ["video","audio","datachannel"] } }
```

### `offer`
SDP offer endereçada a outro peer (P2P) ou ao SFU (`to: "sfu"`).

```json
{ "t": "offer", "data": { "to": "<peerId|sfu>", "sdp": "v=0\r\n..." } }
```

### `answer`

```json
{ "t": "answer", "data": { "to": "<peerId|sfu>", "sdp": "v=0\r\n..." } }
```

### `ice`
Trickle ICE candidate. `candidate: null` sinaliza fim do gathering.

```json
{ "t": "ice", "data": { "to": "<peerId|sfu>", "candidate": "candidate:...", "sdpMid": "0", "sdpMLineIndex": 0 } }
```

### `leave`
Saída voluntária. Fecha o WebSocket em seguida.

```json
{ "t": "leave" }
```

## Mensagens servidor → cliente

### `welcome`
Resposta ao `hello`. Inclui peerId atribuído, peers já presentes e credenciais TURN efêmeras.

```json
{
  "t": "welcome",
  "data": {
    "peerId": "p_xyz",
    "room": "<roomId>",
    "peers": [{ "id": "p_abc", "role": "publisher" }],
    "iceServers": [
      { "urls": ["stun:stun.example.com:3478"] },
      { "urls": ["turn:turn.example.com:3478?transport=udp"], "username": "1735680000:p_xyz", "credential": "<hmac-sha1-base64>" }
    ]
  }
}
```

### `peer-join` / `peer-leave`

```json
{ "t": "peer-join",  "data": { "peer": { "id": "p_abc", "role": "publisher" } } }
{ "t": "peer-leave", "data": { "peerId": "p_abc" } }
```

### `offer` / `answer` / `ice`
Mesmo formato do cliente, com `from` em vez de `to`.

```json
{ "t": "offer", "data": { "from": "p_abc", "sdp": "..." } }
```

### `error`

```json
{ "t": "error", "data": { "code": "INVALID_TOKEN", "message": "..." } }
```

Códigos de erro:

| code              | quando                                       |
| ----------------- | -------------------------------------------- |
| `INVALID_TOKEN`   | JWT ausente, malformado ou expirado          |
| `ROOM_FULL`       | limite de peers atingido                     |
| `PEER_NOT_FOUND`  | `to` aponta para peer inexistente            |
| `RATE_LIMIT`      | excesso de mensagens                         |
| `INTERNAL`        | falha do servidor (com `id` se aplicável)    |

## Regras

- Servidor é **stateless por mensagem** exceto pelo registro de peers da sala.
- Servidor **não inspeciona SDP**. Repassa byte a byte para `to`.
- Mensagens fora de ordem (ex.: `offer` antes do `welcome`) → `error` com `code: "INVALID"`.
- Ping/pong: o servidor envia ping WS a cada 20s; sem pong em 10s, fecha.
- Tamanho máximo de frame: 256 KB.
