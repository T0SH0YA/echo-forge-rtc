// PeerLink = uma RTCPeerConnection contra UM peer remoto. Aplica
// "perfect negotiation" (https://w3c.github.io/webrtc-pc/#perfect-negotiation-example):
// um lado é "polite" (cede em colisão), o outro é "impolite".
import { DataChannel, type DataChannelOptions } from "./data-channel";
import { RemoteTrack } from "./track";
import type { SignalingOut } from "./types";

export interface PeerLinkCallbacks {
  send: (msg: SignalingOut) => void;
  onTrack: (track: RemoteTrack, stream: MediaStream) => void;
  onTrackRemoved?: (track: RemoteTrack) => void;
  onDataChannel?: (channel: DataChannel) => void;
  onConnectionStateChange?: (state: RTCPeerConnectionState) => void;
}

export class PeerLink {
  readonly pc: RTCPeerConnection;
  private makingOffer = false;
  private ignoreOffer = false;
  private pendingCandidates: RTCIceCandidateInit[] = [];
  private channels = new Map<string, DataChannel>();
  private restartScheduled = false;

  constructor(
    readonly remotePeerId: string,
    private readonly polite: boolean,
    iceServers: RTCIceServer[],
    private readonly cb: PeerLinkCallbacks,
  ) {
    this.pc = new RTCPeerConnection({ iceServers });

    this.pc.onicecandidate = ({ candidate }) => {
      cb.send({
        t: "ice",
        data: {
          to: remotePeerId,
          candidate: candidate?.candidate ?? null,
          sdpMid: candidate?.sdpMid ?? null,
          sdpMLineIndex: candidate?.sdpMLineIndex ?? null,
        },
      });
    };

    this.pc.onnegotiationneeded = async () => {
      try {
        this.makingOffer = true;
        await this.pc.setLocalDescription();
        cb.send({ t: "offer", data: { to: remotePeerId, sdp: this.pc.localDescription!.sdp } });
      } catch (err) {
        console.error("[sdk] negotiationneeded", err);
      } finally {
        this.makingOffer = false;
      }
    };

    this.pc.ontrack = (ev) => {
      const stream = ev.streams[0] ?? new MediaStream([ev.track]);
      const rt = new RemoteTrack(ev.track.id, ev.track.kind as "audio" | "video", ev.track, remotePeerId);
      cb.onTrack(rt, stream);
      ev.track.onended = () => cb.onTrackRemoved?.(rt);
    };

    this.pc.ondatachannel = (ev) => {
      const ch = new DataChannel(ev.channel.label, remotePeerId, ev.channel);
      this.channels.set(ev.channel.label, ch);
      cb.onDataChannel?.(ch);
    };

    this.pc.onconnectionstatechange = () => {
      cb.onConnectionStateChange?.(this.pc.connectionState);
      // ICE restart automático em falha (endurecimento)
      if (
        (this.pc.connectionState === "failed" || this.pc.connectionState === "disconnected") &&
        !this.restartScheduled
      ) {
        this.restartScheduled = true;
        setTimeout(() => this.tryIceRestart(), 1500);
      }
      if (this.pc.connectionState === "connected") {
        this.restartScheduled = false;
      }
    };
  }

  private async tryIceRestart(): Promise<void> {
    if (this.pc.connectionState === "connected" || this.pc.connectionState === "closed") {
      this.restartScheduled = false;
      return;
    }
    if (this.polite) {
      // só o lado impolite reinicia, evita colisão dupla
      this.restartScheduled = false;
      return;
    }
    try {
      this.makingOffer = true;
      await this.pc.setLocalDescription(await this.pc.createOffer({ iceRestart: true }));
      this.cb.send({ t: "offer", data: { to: this.remotePeerId, sdp: this.pc.localDescription!.sdp } });
    } catch (err) {
      console.warn("[sdk] ice restart", err);
    } finally {
      this.makingOffer = false;
      this.restartScheduled = false;
    }
  }

  addLocalTrack(track: MediaStreamTrack, stream: MediaStream): RTCRtpSender {
    return this.pc.addTrack(track, stream);
  }

  openDataChannel(label: string, opts: DataChannelOptions = {}): DataChannel {
    const existing = this.channels.get(label);
    if (existing) return existing;
    const init: RTCDataChannelInit = {
      ordered: opts.ordered ?? true,
      maxRetransmits: opts.maxRetransmits,
      maxPacketLifeTime: opts.maxPacketLifeTime,
      protocol: opts.protocol,
      negotiated: opts.negotiated,
      id: opts.id,
    };
    const dc = this.pc.createDataChannel(label, init);
    const ch = new DataChannel(label, this.remotePeerId, dc);
    this.channels.set(label, ch);
    // Notifica também para canais criados localmente, para que o Room
    // possa escutar "message" e propagar como evento "data".
    this.cb.onDataChannel?.(ch);
    return ch;
  }

  getDataChannel(label: string): DataChannel | undefined {
    return this.channels.get(label);
  }

  async handleOffer(sdp: string): Promise<void> {
    const offerCollision = this.makingOffer || this.pc.signalingState !== "stable";
    this.ignoreOffer = !this.polite && offerCollision;
    if (this.ignoreOffer) return;

    await this.pc.setRemoteDescription({ type: "offer", sdp });
    await this.flushCandidates();
    await this.pc.setLocalDescription();
    this.cb.send({ t: "answer", data: { to: this.remotePeerId, sdp: this.pc.localDescription!.sdp } });
  }

  async handleAnswer(sdp: string): Promise<void> {
    if (this.pc.signalingState !== "have-local-offer") return;
    await this.pc.setRemoteDescription({ type: "answer", sdp });
    await this.flushCandidates();
  }

  async handleIce(candidate: string | null, sdpMid: string | null | undefined, sdpMLineIndex: number | null | undefined): Promise<void> {
    const init: RTCIceCandidateInit = candidate === null ? { candidate: "" } : { candidate, sdpMid: sdpMid ?? undefined, sdpMLineIndex: sdpMLineIndex ?? undefined };
    if (!this.pc.remoteDescription) {
      this.pendingCandidates.push(init);
      return;
    }
    try {
      await this.pc.addIceCandidate(init);
    } catch (err) {
      if (!this.ignoreOffer) console.warn("[sdk] addIceCandidate", err);
    }
  }

  private async flushCandidates(): Promise<void> {
    const pending = this.pendingCandidates;
    this.pendingCandidates = [];
    for (const c of pending) {
      try {
        await this.pc.addIceCandidate(c);
      } catch (err) {
        console.warn("[sdk] flush candidate", err);
      }
    }
  }

  close(): void {
    for (const ch of this.channels.values()) ch.close();
    this.channels.clear();
    this.pc.close();
  }
}
