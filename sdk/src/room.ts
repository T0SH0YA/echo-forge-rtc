import { Emitter } from "./emitter";
import { LocalTrack, RemoteTrack } from "./track";
import type {
  ConnectionState,
  LocalTrackBundle,
  RemotePeer,
  SignalingIn,
  SignalingOut,
} from "./types";

interface RoomEvents {
  "peer-joined": RemotePeer;
  "peer-left": { peerId: string };
  "track-subscribed": { peer: RemotePeer; track: RemoteTrack; stream: MediaStream };
  "track-unsubscribed": { peer: RemotePeer; track: RemoteTrack };
  "connection-state": ConnectionState;
  error: Error;
}

const SDK_VERSION = "0.0.1";

/**
 * Etapa 1: esqueleto. A lógica real de SDP/ICE/PeerConnection entra na Etapa 2.
 * O que existe aqui já: ciclo de vida do WebSocket, parser de envelope, eventos públicos.
 */
export class Room extends Emitter<RoomEvents> {
  readonly id: string;
  localPeerId = "";
  readonly peers = new Map<string, RemotePeer>();

  private ws: WebSocket | null = null;
  private iceServers: RTCIceServer[] = [];
  private state: ConnectionState = "connecting";

  constructor(id: string, private readonly url: string, private readonly token: string) {
    super();
    this.id = id;
  }

  async connect(): Promise<void> {
    const wsUrl = `${this.url.replace(/\/$/, "")}/v1/rooms/${encodeURIComponent(this.id)}?token=${encodeURIComponent(this.token)}`;
    this.ws = new WebSocket(wsUrl);

    await new Promise<void>((resolve, reject) => {
      const ws = this.ws!;
      ws.onopen = () => {
        this.send({ t: "hello", data: { sdkVersion: SDK_VERSION, capabilities: ["video", "audio", "datachannel"] } });
      };
      ws.onmessage = (ev) => {
        let msg: SignalingIn;
        try {
          msg = JSON.parse(typeof ev.data === "string" ? ev.data : "");
        } catch {
          return;
        }
        if (msg.t === "welcome") {
          this.localPeerId = msg.data.peerId;
          this.iceServers = msg.data.iceServers;
          for (const p of msg.data.peers) {
            this.peers.set(p.id, { id: p.id, role: p.role, tracks: new Map() });
          }
          this.setState("connected");
          resolve();
          return;
        }
        this.handleMessage(msg);
      };
      ws.onerror = () => reject(new Error("websocket error"));
      ws.onclose = () => {
        this.setState("closed");
      };
    });
  }

  private handleMessage(msg: SignalingIn): void {
    switch (msg.t) {
      case "peer-join": {
        const peer: RemotePeer = { id: msg.data.peer.id, role: msg.data.peer.role, tracks: new Map() };
        this.peers.set(peer.id, peer);
        this.emit("peer-joined", peer);
        return;
      }
      case "peer-leave": {
        this.peers.delete(msg.data.peerId);
        this.emit("peer-left", { peerId: msg.data.peerId });
        return;
      }
      case "offer":
      case "answer":
      case "ice":
        // Etapa 2 — alimenta o RTCPeerConnection correto
        return;
      case "error":
        this.emit("error", new Error(`[${msg.data.code}] ${msg.data.message}`));
        return;
    }
  }

  private send(msg: SignalingOut): void {
    this.ws?.send(JSON.stringify(msg));
  }

  private setState(s: ConnectionState): void {
    if (this.state === s) return;
    this.state = s;
    this.emit("connection-state", s);
  }

  // ----- API pública (stubs Etapa 1; implementação real Etapa 2) -----

  async publishCamera(_constraints?: MediaStreamConstraints): Promise<LocalTrackBundle> {
    throw new Error("publishCamera: implementado na Etapa 2");
  }

  async publishScreen(): Promise<LocalTrackBundle> {
    throw new Error("publishScreen: implementado na Etapa 2");
  }

  async publishTrack(_track: MediaStreamTrack): Promise<LocalTrack> {
    throw new Error("publishTrack: implementado na Etapa 2");
  }

  async unpublish(_track: LocalTrack): Promise<void> {
    throw new Error("unpublish: implementado na Etapa 2");
  }

  async leave(): Promise<void> {
    try {
      this.send({ t: "leave" });
    } catch {
      // ignore
    }
    this.ws?.close();
    this.ws = null;
    this.setState("closed");
  }

  /** Disponibiliza os ICE servers recebidos no welcome (usado internamente pelas PeerConnections). */
  getIceServers(): RTCIceServer[] {
    return this.iceServers;
  }
}
