# SDK — API pública

Pacote: `@webrtc-own/sdk` (nome provisório). ESM + CJS. TypeScript first.

## Instalação

```bash
npm i @webrtc-own/sdk
```

## Uso mínimo

```ts
import { Client } from "@webrtc-own/sdk";

const client = new Client({ url: "wss://signal.example.com" });

const room = await client.join({ roomId: "demo", token: "<jwt>" });

const local = await room.publishCamera({ video: true, audio: true });

room.on("peer-joined", (peer) => console.log("entrou", peer.id));
room.on("track-subscribed", ({ peer, track, stream }) => {
  document.querySelector<HTMLVideoElement>("#remote")!.srcObject = stream;
});

// depois...
await room.leave();
```

## Classes

### `Client`
- `new Client(opts: { url: string })`
- `join(opts: { roomId: string; token: string }): Promise<Room>`

### `Room` (extends EventEmitter)
- `id: string`
- `localPeerId: string`
- `peers: Map<string, RemotePeer>`
- `publishCamera(constraints?: MediaStreamConstraints): Promise<LocalTrackBundle>`
- `publishScreen(): Promise<LocalTrackBundle>`
- `publishTrack(track: MediaStreamTrack): Promise<LocalTrack>`
- `unpublish(track: LocalTrack): Promise<void>`
- `leave(): Promise<void>`

Eventos:

| evento              | payload                                          |
| ------------------- | ------------------------------------------------ |
| `peer-joined`       | `RemotePeer`                                     |
| `peer-left`         | `{ peerId: string }`                             |
| `track-subscribed`  | `{ peer: RemotePeer; track: RemoteTrack; stream: MediaStream }` |
| `track-unsubscribed`| `{ peer: RemotePeer; track: RemoteTrack }`       |
| `connection-state`  | `"connecting" \| "connected" \| "reconnecting" \| "closed"` |
| `error`             | `Error`                                          |

### `LocalTrack` / `RemoteTrack`
- `id: string`
- `kind: "audio" | "video"`
- `mediaStreamTrack: MediaStreamTrack`
- `mute(): void` / `unmute(): void`
- `stop(): void`

### `RemotePeer`
- `id: string`
- `tracks: Map<string, RemoteTrack>`

## Estados de conexão

```text
connecting → connected → (reconnecting → connected)* → closed
```

Reconexão automática usa ICE restart sem fechar a `Room`.
