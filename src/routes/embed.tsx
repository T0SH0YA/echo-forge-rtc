// Rota /embed — versão pra ser carregada dentro de <iframe> no app da Teli.
// Sem lobby, sem header, entra direto na sala usando querystring.
// Comunica com o parent via postMessage (src/lib/embed-bridge.ts).
//
// Querystring:
//   room       (obrigatório) id da sala
//   token      (obrigatório) JWT emitido pelo backend Teli via /api/public/token
//   name       (opcional) nome pra exibir no tile
//   theme      (opcional) "dark" | "light" — default segue prefers-color-scheme
//
// Ex: /embed?room=abc&token=eyJhbGciOi...&name=Rafael

import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { Client, type Room } from "../../sdk/src";
import {
  createEmbedBridge,
  DEFAULT_ALLOWED_ORIGINS,
  type EmbedBridge,
} from "@/lib/embed-bridge";

interface EmbedSearch {
  room?: string;
  token?: string;
  name?: string;
  theme?: "dark" | "light";
}

export const Route = createFileRoute("/embed")({
  head: () => ({
    meta: [
      { title: "Sala embed" },
      { name: "robots", content: "noindex,nofollow" },
    ],
  }),
  validateSearch: (s: Record<string, unknown>): EmbedSearch => ({
    room: typeof s.room === "string" ? s.room : undefined,
    token: typeof s.token === "string" ? s.token : undefined,
    name: typeof s.name === "string" ? s.name : undefined,
    theme: s.theme === "light" || s.theme === "dark" ? s.theme : undefined,
  }),
  component: EmbedRoom,
});

interface Remote {
  id: string;
  peerId: string;
  stream: MediaStream;
}

function getRemoteId(peerId: string, stream: MediaStream) {
  return `${peerId}:${stream.id}`;
}

function EmbedRoom() {
  const { room: roomId, token, name, theme } = Route.useSearch();
  const [remotes, setRemotes] = useState<Remote[]>([]);
  const [micOn, setMicOn] = useState(true);
  const [camOn, setCamOn] = useState(true);
  const [status, setStatus] = useState<"joining" | "joined" | "error" | "left">("joining");
  const [errMsg, setErrMsg] = useState("");

  const localVideoRef = useRef<HTMLVideoElement>(null);
  const localStreamRef = useRef<MediaStream | null>(null);
  const roomRef = useRef<Room | null>(null);
  const bridgeRef = useRef<EmbedBridge | null>(null);

  const signalingUrl =
    (import.meta.env.VITE_SIGNALING_URL as string | undefined) || "wss://sig.teli.app.br";

  useEffect(() => {
    if (theme === "dark") document.documentElement.classList.add("dark");
    if (theme === "light") document.documentElement.classList.remove("dark");
  }, [theme]);

  useEffect(() => {
    const bridge = createEmbedBridge({ allowedOrigins: DEFAULT_ALLOWED_ORIGINS });
    bridgeRef.current = bridge;
    bridge.emit({ t: "ready" });

    const offMute = bridge.on("mute", () => applyMic(false));
    const offUnmute = bridge.on("unmute", () => applyMic(true));
    const offCamOff = bridge.on("camera-off", () => applyCam(false));
    const offCamOn = bridge.on("camera-on", () => applyCam(true));
    const offLeave = bridge.on("leave", () => void leave());

    return () => {
      offMute(); offUnmute(); offCamOff(); offCamOn(); offLeave();
      bridge.destroy();
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!roomId || !token) {
      setStatus("error");
      setErrMsg("faltando ?room= ou ?token=");
      bridgeRef.current?.emit({
        t: "error",
        message: "missing room/token in querystring",
      });
      return;
    }
    void join();
    return () => {
      void roomRef.current?.leave();
      localStreamRef.current?.getTracks().forEach((t) => t.stop());
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [roomId, token]);

  async function join() {
    try {
      let stream: MediaStream;
      try {
        stream = await navigator.mediaDevices.getUserMedia({ video: true, audio: true });
      } catch (err) {
        bridgeRef.current?.emit({ t: "permission-denied", kind: "both" });
        throw err;
      }
      localStreamRef.current = stream;
      if (localVideoRef.current) {
        localVideoRef.current.srcObject = stream;
        await localVideoRef.current.play().catch(() => {});
      }

      const client = new Client({ url: signalingUrl });
      const r = await client.join({ roomId: roomId!, token: token! });
      roomRef.current = r;

      r.on("peer-joined", (peer) => {
        bridgeRef.current?.emit({ t: "peer-joined", peerId: peer.id });
      });
      r.on("peer-left", ({ peerId }) => {
        setRemotes((cur) => cur.filter((x) => x.peerId !== peerId));
        bridgeRef.current?.emit({ t: "peer-left", peerId });
      });
      r.on("track-subscribed", ({ peer, track, stream: rs }) => {
        setRemotes((cur) => {
          const id = getRemoteId(peer.id, rs);
          const ex = cur.find((x) => x.id === id);
          if (ex) {
            if (!ex.stream.getTracks().includes(track.mediaStreamTrack)) {
              ex.stream.addTrack(track.mediaStreamTrack);
            }
            return [...cur];
          }
          return [...cur, { id, peerId: peer.id, stream: rs }];
        });
      });
      r.on("track-unsubscribed", ({ peer, track }) => {
        setRemotes((cur) =>
          cur.flatMap((entry) => {
            if (entry.peerId !== peer.id || !entry.stream.getTracks().includes(track.mediaStreamTrack)) return [entry];
            entry.stream.removeTrack(track.mediaStreamTrack);
            return entry.stream.getTracks().length > 0 ? [entry] : [];
          }),
        );
      });

      await r.publishCamera({ video: true, audio: true });
      setStatus("joined");
      bridgeRef.current?.emit({ t: "joined", peerId: r.localPeerId, room: roomId! });
    } catch (err) {
      const msg = (err as Error).message;
      setStatus("error");
      setErrMsg(msg);
      bridgeRef.current?.emit({ t: "error", message: msg });
    }
  }

  async function leave() {
    await roomRef.current?.leave();
    roomRef.current = null;
    localStreamRef.current?.getTracks().forEach((t) => t.stop());
    localStreamRef.current = null;
    if (localVideoRef.current) localVideoRef.current.srcObject = null;
    setRemotes([]);
    setStatus("left");
    bridgeRef.current?.emit({ t: "left" });
  }

  function applyMic(enabled: boolean) {
    const s = localStreamRef.current;
    if (!s) return;
    s.getAudioTracks().forEach((t) => (t.enabled = enabled));
    setMicOn(enabled);
    bridgeRef.current?.emit({ t: "device-changed", kind: "audio", enabled });
  }
  function applyCam(enabled: boolean) {
    const s = localStreamRef.current;
    if (!s) return;
    s.getVideoTracks().forEach((t) => (t.enabled = enabled));
    setCamOn(enabled);
    bridgeRef.current?.emit({ t: "device-changed", kind: "video", enabled });
  }

  if (status === "error") {
    return (
      <div className="flex h-screen items-center justify-center bg-black text-white p-4 text-center text-sm">
        Não foi possível entrar: {errMsg}
      </div>
    );
  }
  if (status === "left") {
    return (
      <div className="flex h-screen items-center justify-center bg-black text-white text-sm">
        Chamada encerrada.
      </div>
    );
  }

  const tiles = [
    { id: "local", stream: localStreamRef.current, label: `${name || "Você"}`, local: true },
    ...remotes.map((r, _index, all) => {
      const peerStreams = all.filter((entry) => entry.peerId === r.peerId);
      const isExtraVideoStream = r.stream.getVideoTracks().length > 0 && peerStreams.findIndex((entry) => entry.id === r.id) > 0;
      return {
        id: r.id,
        stream: r.stream,
        label: isExtraVideoStream ? `Tela de ${r.peerId}` : r.peerId,
        local: false,
      };
    }),
  ];
  const cols =
    tiles.length === 1
      ? "grid-cols-1"
      : tiles.length === 2
      ? "grid-cols-1 sm:grid-cols-2"
      : "grid-cols-2 lg:grid-cols-3";

  return (
    <div className="flex h-screen flex-col bg-black text-white">
      <main className="flex-1 overflow-auto p-2">
        <div className={`grid gap-2 ${cols}`}>
          {tiles.map((t) => (
            <div
              key={t.id}
              className="relative aspect-video overflow-hidden rounded-lg bg-neutral-900 ring-1 ring-white/10"
            >
              <VideoEl stream={t.stream} muted={t.local} />
              <div className="absolute bottom-1.5 left-1.5 rounded bg-black/60 px-2 py-0.5 text-xs">
                {t.label}
              </div>
            </div>
          ))}
        </div>
      </main>
      <footer className="flex items-center justify-center gap-2 border-t border-white/10 px-3 py-2">
        <CtrlBtn active={micOn} onClick={() => applyMic(!micOn)} label={micOn ? "Mic" : "Mic off"} />
        <CtrlBtn active={camOn} onClick={() => applyCam(!camOn)} label={camOn ? "Cam" : "Cam off"} />
        <button
          onClick={() => void leave()}
          className="rounded-full bg-red-600 px-4 py-1.5 text-sm font-medium hover:bg-red-700"
        >
          Sair
        </button>
      </footer>
    </div>
  );
}

function VideoEl({ stream, muted }: { stream: MediaStream | null; muted?: boolean }) {
  const ref = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    if (ref.current && stream) {
      ref.current.srcObject = stream;
      void ref.current.play().catch(() => {});
    }
  }, [stream]);
  return (
    <video
      ref={ref}
      autoPlay
      playsInline
      muted={muted}
      className="h-full w-full object-cover bg-black"
    />
  );
}

function CtrlBtn({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-full px-3 py-1.5 text-xs font-medium transition ${
        active ? "bg-white/10 hover:bg-white/20" : "bg-red-600 hover:bg-red-700"
      }`}
    >
      {label}
    </button>
  );
}
