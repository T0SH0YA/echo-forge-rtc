export class LocalTrack {
  constructor(
    public readonly id: string,
    public readonly kind: "audio" | "video",
    public readonly mediaStreamTrack: MediaStreamTrack,
  ) {}

  mute(): void {
    this.mediaStreamTrack.enabled = false;
  }

  unmute(): void {
    this.mediaStreamTrack.enabled = true;
  }

  stop(): void {
    this.mediaStreamTrack.stop();
  }
}

export class RemoteTrack {
  constructor(
    public readonly id: string,
    public readonly kind: "audio" | "video",
    public readonly mediaStreamTrack: MediaStreamTrack,
    public readonly peerId: string,
  ) {}

  stop(): void {
    this.mediaStreamTrack.stop();
  }
}
