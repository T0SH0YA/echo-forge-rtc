export interface ClientOptions {
  url: string;
}

export interface JoinOptions {
  roomId: string;
  token: string;
}

export interface RemotePeer {
  id: string;
  role?: string;
  tracks: Map<string, import("./track").RemoteTrack>;
}

export interface LocalTrackBundle {
  audio?: import("./track").LocalTrack;
  video?: import("./track").LocalTrack;
  stream: MediaStream;
}

export type ConnectionState = "connecting" | "connected" | "reconnecting" | "closed";

// Sinalização — espelho de docs/protocol/signaling.md
export type SignalingOut =
  | { t: "hello"; data: { sdkVersion: string; capabilities: string[] } }
  | { t: "offer"; data: { to: string; sdp: string } }
  | { t: "answer"; data: { to: string; sdp: string } }
  | { t: "ice"; data: { to: string; candidate: string | null; sdpMid?: string | null; sdpMLineIndex?: number | null } }
  | { t: "leave" };

export type SignalingIn =
  | { t: "welcome"; data: { peerId: string; room: string; peers: Array<{ id: string; role?: string }>; iceServers: RTCIceServer[] } }
  | { t: "peer-join"; data: { peer: { id: string; role?: string } } }
  | { t: "peer-leave"; data: { peerId: string } }
  | { t: "offer"; data: { from: string; sdp: string } }
  | { t: "answer"; data: { from: string; sdp: string } }
  | { t: "ice"; data: { from: string; candidate: string | null; sdpMid?: string | null; sdpMLineIndex?: number | null } }
  | { t: "error"; data: { code: string; message: string } };
