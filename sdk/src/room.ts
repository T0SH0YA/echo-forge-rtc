import { Emitter } from "./emitter";
import { PeerLink } from "./peer-link";
import { LocalTrack, RemoteTrack } from "./track";
import { openTransport, type SignalingTransport } from "./transport";
import type {
  ConnectionState,
  LocalTrackBundle,
  RemotePeer,
  SignalingIn,
  SignalingOut,
} from "./types";

interface RoomEvents extends Record<string, unknown> {
  "peer-joined": RemotePeer;
  "peer-left": { peerId: string };
  "track-subscribed": { peer: RemotePeer; track: RemoteTrack; stream: MediaStream };
  "track-unsubscribed": { peer: RemotePeer; track: RemoteTrack };
  "connection-state": ConnectionState;
  error: Error;
}

const SDK_VERSION = "0.1.0";

/**
 * Sala em malha (full-mesh P2P). Cada peer mantém um RTCPeerConnection por
 * peer remoto. A política de "polite peer" é decidida pela ordem lexicográfica
 * dos peerIds (id menor = impolite).
 *
 * Etapa 2: P2P só. Etapa 5+ vai introduzir o modo SFU como alternativa.
 */
export class Room extends Emitter<RoomEvents> {
  readonly id: string;
  localPeerId = "";
  readonly peers = new Map<string, RemotePeer>();

  private transport: SignalingTransport | null = null;
  private iceServers: RTCIceServer[] = [];
  private links = new Map<string, PeerLink>();
  private localStream: MediaStream | null = null;
  private localTracks: LocalTrack[] = [];
  private state: ConnectionState = "connecting";

  constructor(id: string, private readonly url: string, private readonly token: string) {
    super();
    this.id = id;
  }

  async connect(): Promise<void> {
    const transport = await openTransport(this.url, this.id, this.token);
    this.transport = transport;

    transport.onClose(() => this.setState("closed"));

    return new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error("welcome timeout")), 10_000);
      transport.onMessage((msg) => {
        if (msg.t === "welcome") {
          clearTimeout(timeout);
          this.localPeerId = msg.data.peerId;
          this.iceServers = msg.data.iceServers;
          for (const p of msg.data.peers) {
            const peer: RemotePeer = { id: p.id, role: p.role, tracks: new Map() };
            this.peers.set(p.id, peer);
            // já presente: nós (que acabamos de chegar) começamos a negociação
            this.ensureLink(p.id, /*initiate=*/ true);
            this.emit("peer-joined", peer);
          }
          this.setState("connected");
          resolve();
          return;
        }
        this.handleMessage(msg);
      });
      transport.send({ t: "hello", data: { sdkVersion: SDK_VERSION, capabilities: ["video", "audio"] } });
    });
  }

  private handleMessage(msg: SignalingIn): void {
    switch (msg.t) {
      case "peer-join": {
        const peer: RemotePeer = { id: msg.data.peer.id, role: msg.data.peer.role, tracks: new Map() };
        this.peers.set(peer.id, peer);
        // peer novo: ele que inicia (nós só respondemos), mas se já tivermos
        // tracks publicadas precisamos garantir um link pra propagá-las.
        if (this.localTracks.length > 0) {
          this.ensureLink(peer.id, /*initiate=*/ true);
        }
        this.emit("peer-joined", peer);
        return;
      }
      case "peer-leave": {
        const id = msg.data.peerId;
        this.links.get(id)?.close();
        this.links.delete(id);
        this.peers.delete(id);
        this.emit("peer-left", { peerId: id });
        return;
      }
      case "offer": {
        const link = this.ensureLink(msg.data.from, /*initiate=*/ false);
        void link.handleOffer(msg.data.sdp);
        return;
      }
      case "answer": {
        const link = this.links.get(msg.data.from);
        if (link) void link.handleAnswer(msg.data.sdp);
        return;
      }
      case "ice": {
        const link = this.links.get(msg.data.from);
        if (link) void link.handleIce(msg.data.candidate, msg.data.sdpMid, msg.data.sdpMLineIndex);
        return;
      }
      case "error":
        this.emit("error", new Error(`[${msg.data.code}] ${msg.data.message}`));
        return;
    }
  }

  private ensureLink(remoteId: string, initiate: boolean): PeerLink {
    let link = this.links.get(remoteId);
    if (link) return link;

    // Polite = id maior. Empate impossível (ids únicos do servidor / random no BC).
    const polite = this.localPeerId > remoteId;

    link = new PeerLink(remoteId, polite, this.iceServers, {
      send: (msg) => this.send(msg),
      onTrack: (track, stream) => {
        const peer = this.peers.get(remoteId);
        if (!peer) return;
        peer.tracks.set(track.id, track);
        this.emit("track-subscribed", { peer, track, stream });
      },
      onTrackRemoved: (track) => {
        const peer = this.peers.get(remoteId);
        if (!peer) return;
        peer.tracks.delete(track.id);
        this.emit("track-unsubscribed", { peer, track });
      },
    });
    this.links.set(remoteId, link);

    // Se já temos tracks locais, adiciona pra disparar negotiationneeded
    if (this.localStream) {
      for (const t of this.localStream.getTracks()) {
        link.addLocalTrack(t, this.localStream);
      }
    }

    // `initiate` é informativo: a negociação real é disparada por
    // `onnegotiationneeded` ao adicionar tracks. Se nenhum lado tem tracks
    // ainda, ninguém inicia — ok, links sobem quando o primeiro publicar.
    void initiate;

    return link;
  }

  private send(msg: SignalingOut): void {
    this.transport?.send(msg);
  }

  private setState(s: ConnectionState): void {
    if (this.state === s) return;
    this.state = s;
    this.emit("connection-state", s);
  }

  // ---------- API pública ----------

  async publishCamera(constraints: MediaStreamConstraints = { video: true, audio: true }): Promise<LocalTrackBundle> {
    const stream = await navigator.mediaDevices.getUserMedia(constraints);
    return this.publishStream(stream);
  }

  async publishScreen(): Promise<LocalTrackBundle> {
    const stream = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: true });
    return this.publishStream(stream);
  }

  async publishTrack(track: MediaStreamTrack): Promise<LocalTrack> {
    const stream = this.localStream ?? new MediaStream();
    stream.addTrack(track);
    this.localStream = stream;
    const local = new LocalTrack(track.id, track.kind as "audio" | "video", track);
    this.localTracks.push(local);
    for (const link of this.links.values()) {
      link.addLocalTrack(track, stream);
    }
    // garante link com todos os peers conhecidos
    for (const peerId of this.peers.keys()) {
      this.ensureLink(peerId, true);
      const link = this.links.get(peerId)!;
      if (!link.pc.getSenders().some((s) => s.track === track)) {
        link.addLocalTrack(track, stream);
      }
    }
    return local;
  }

  private async publishStream(stream: MediaStream): Promise<LocalTrackBundle> {
    this.localStream = this.localStream ?? new MediaStream();
    const bundle: LocalTrackBundle = { stream: this.localStream };
    for (const t of stream.getTracks()) {
      this.localStream.addTrack(t);
      const local = new LocalTrack(t.id, t.kind as "audio" | "video", t);
      this.localTracks.push(local);
      if (t.kind === "audio") bundle.audio = local;
      else bundle.video = local;
    }
    // garante link + tracks com cada peer conhecido
    for (const peerId of this.peers.keys()) {
      const link = this.ensureLink(peerId, true);
      for (const t of stream.getTracks()) {
        if (!link.pc.getSenders().some((s) => s.track === t)) {
          link.addLocalTrack(t, this.localStream);
        }
      }
    }
    return bundle;
  }

  async unpublish(track: LocalTrack): Promise<void> {
    this.localTracks = this.localTracks.filter((t) => t !== track);
    this.localStream?.removeTrack(track.mediaStreamTrack);
    for (const link of this.links.values()) {
      const sender = link.pc.getSenders().find((s) => s.track === track.mediaStreamTrack);
      if (sender) link.pc.removeTrack(sender);
    }
    track.stop();
  }

  async leave(): Promise<void> {
    try {
      this.send({ t: "leave" });
    } catch {
      // ignore
    }
    for (const link of this.links.values()) link.close();
    this.links.clear();
    this.localStream?.getTracks().forEach((t) => t.stop());
    this.localStream = null;
    this.localTracks = [];
    this.transport?.close();
    this.transport = null;
    this.setState("closed");
  }

  getIceServers(): RTCIceServer[] {
    return this.iceServers;
  }
}
