// PeerLink = uma RTCPeerConnection contra UM peer remoto. Aplica
// "perfect negotiation" (https://w3c.github.io/webrtc-pc/#perfect-negotiation-example):
// um lado é "polite" (cede em colisão), o outro é "impolite".
import { RemoteTrack } from "./track";
import type { SignalingOut } from "./types";

export interface PeerLinkCallbacks {
  send: (msg: SignalingOut) => void;
  onTrack: (track: RemoteTrack, stream: MediaStream) => void;
  onTrackRemoved?: (track: RemoteTrack) => void;
}

export class PeerLink {
  readonly pc: RTCPeerConnection;
  private makingOffer = false;
  private ignoreOffer = false;
  private pendingCandidates: RTCIceCandidateInit[] = [];

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
  }

  addLocalTrack(track: MediaStreamTrack, stream: MediaStream): RTCRtpSender {
    return this.pc.addTrack(track, stream);
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
    this.pc.close();
  }
}
