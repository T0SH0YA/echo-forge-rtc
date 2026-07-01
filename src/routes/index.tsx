import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { useChat } from "../hooks/useChat";
import { ChatPanel } from "../components/ChatPanel";
import { Client, type Room, type RemoteTrack } from "../../sdk/src";

export const Route = createFileRoute("/")({
  head: () => ({
    meta: [
      { title: "Sala — WebRTC próprio" },
      {
        name: "description",
        content:
          "Sala de videoconferência rodando 100% sobre stack WebRTC próprio (sinalização, STUN, TURN, SFU, SRTP).",
      },
    ],
  }),
  component: MeetingRoom,
});

type Phase = "lobby" | "joining" | "in-call" | "ended" | "error";

interface RemoteEntry {
  peerId: string;
  stream: MediaStream;
}

function getInitialRoomId() {
  if (typeof window === "undefined") return "demo";
  const u = new URL(window.location.href);
  return u.searchParams.get("room") || makeRoomId();
}

function makeRoomId() {
  return Math.random().toString(36).slice(2, 8);
}

function MeetingRoom() {
  const [phase, setPhase] = useState<Phase>("lobby");
  const [roomId, setRoomId] = useState(getInitialRoomId);
  const [name, setName] = useState("Você");
  const [errMsg, setErrMsg] = useState("");
  const [remotes, setRemotes] = useState<RemoteEntry[]>([]);
  const [micOn, setMicOn] = useState(true);
  const [camOn, setCamOn] = useState(true);
  const [room, setRoom] = useState<Room | null>(null);
  const [chatOpen, setChatOpen] = useState(false);
  const chat = useChat(room, name);
  const [copied, setCopied] = useState(false);

  const localVideoRef = useRef<HTMLVideoElement>(null);
  const roomRef = useRef<Room | null>(null);
  const localStreamRef = useRef<MediaStream | null>(null);

  // Signaling: usa VITE_SIGNALING_URL se definido (ex: wss://sig.teli.app.br),
  // senão cai no loopback bc:// (só funciona entre abas do mesmo navegador).
  const signalingUrl =
    (import.meta.env.VITE_SIGNALING_URL as string | undefined) || "bc://lovable-room";

  const shareUrl = useMemo(() => {
    if (typeof window === "undefined") return "";
    const u = new URL(window.location.href);
    u.searchParams.set("room", roomId);
    return u.toString();
  }, [roomId]);

  useEffect(() => {
    return () => {
      void roomRef.current?.leave();
      setRoom(null);
      localStreamRef.current?.getTracks().forEach((t) => t.stop());
    };
  }, []);

  const join = async () => {
    setPhase("joining");
    setErrMsg("");
    try {
      // 1. pega câmera/mic
      const stream = await navigator.mediaDevices.getUserMedia({ video: true, audio: true });
      localStreamRef.current = stream;
      if (localVideoRef.current) {
        localVideoRef.current.srcObject = stream;
        await localVideoRef.current.play().catch(() => {});
      }

      // 2. entra na sala
      const client = new Client({ url: signalingUrl });
      const room = await client.join({ roomId, token: name || "anon" });
      roomRef.current = room;
      setRoom(room);

      room.on("peer-left", ({ peerId }) => {
        setRemotes((r) => r.filter((x) => x.peerId !== peerId));
      });
      room.on("track-subscribed", ({ peer, track, stream: rs }) => {
        setRemotes((r) => {
          const ex = r.find((x) => x.peerId === peer.id);
          if (ex) {
            if (!ex.stream.getTracks().includes(track.mediaStreamTrack)) {
              ex.stream.addTrack(track.mediaStreamTrack);
            }
            return [...r];
          }
          return [...r, { peerId: peer.id, stream: rs }];
        });
      });

      // 3. publica
      await room.publishCamera({ video: true, audio: true });

      setPhase("in-call");
    } catch (err) {
      setErrMsg((err as Error).message);
      setPhase("error");
    }
  };

  const leave = async () => {
    await roomRef.current?.leave();
    roomRef.current = null;
    localStreamRef.current?.getTracks().forEach((t) => t.stop());
    localStreamRef.current = null;
    if (localVideoRef.current) localVideoRef.current.srcObject = null;
    setRemotes([]);
    setPhase("ended");
  };

  const toggleMic = () => {
    const s = localStreamRef.current;
    if (!s) return;
    const next = !micOn;
    s.getAudioTracks().forEach((t) => (t.enabled = next));
    setMicOn(next);
  };

  const toggleCam = () => {
    const s = localStreamRef.current;
    if (!s) return;
    const next = !camOn;
    s.getVideoTracks().forEach((t) => (t.enabled = next));
    setCamOn(next);
  };

  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(shareUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {}
  };

  // ---------- LOBBY ----------
  if (phase === "lobby" || phase === "joining" || phase === "ended" || phase === "error") {
    return (
      <div className="min-h-screen bg-background text-foreground flex items-center justify-center px-4">
        <div className="w-full max-w-md rounded-2xl border border-border bg-card p-8 shadow-sm">
          <h1 className="text-2xl font-semibold tracking-tight">Entrar na sala</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Stack WebRTC 100% próprio. Sem Zoom, sem Meet.
          </p>

          <div className="mt-6 space-y-4">
            <label className="block">
              <span className="mb-1 block text-xs font-medium uppercase tracking-wide text-muted-foreground">
                Seu nome
              </span>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
              />
            </label>

            <label className="block">
              <span className="mb-1 block text-xs font-medium uppercase tracking-wide text-muted-foreground">
                Código da sala
              </span>
              <div className="flex gap-2">
                <input
                  value={roomId}
                  onChange={(e) => setRoomId(e.target.value)}
                  className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
                />
                <button
                  type="button"
                  onClick={() => setRoomId(makeRoomId())}
                  className="rounded-md border border-border px-3 py-2 text-xs hover:bg-muted"
                  title="Gerar nova sala"
                >
                  Nova
                </button>
              </div>
            </label>

            <button
              onClick={join}
              disabled={phase === "joining" || !roomId.trim()}
              className="w-full rounded-md bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              {phase === "joining" ? "Entrando..." : "Entrar na sala"}
            </button>

            {phase === "ended" && (
              <p className="text-xs text-muted-foreground text-center">
                Você saiu. Entre de novo quando quiser.
              </p>
            )}
            {phase === "error" && (
              <p className="text-xs text-red-500 text-center">Erro: {errMsg}</p>
            )}

            <div className="rounded-md bg-muted/50 p-3 text-xs text-muted-foreground leading-relaxed">
              <strong className="text-foreground">Dica:</strong> abra esta página em outra aba (ou
              mande o link) com a mesma sala. As duas abas se conectam P2P, sem servidor de mídia.
            </div>
          </div>
        </div>
      </div>
    );
  }

  // ---------- IN-CALL ----------
  const tiles = [
    { peerId: "local", stream: localStreamRef.current, label: `${name} (você)`, local: true },
    ...remotes.map((r) => ({ peerId: r.peerId, stream: r.stream, label: r.peerId, local: false })),
  ];
  const cols = tiles.length === 1 ? "grid-cols-1" : tiles.length === 2 ? "grid-cols-1 sm:grid-cols-2" : "grid-cols-2 lg:grid-cols-3";

  return (
    <div className="flex h-screen bg-background text-foreground">
      <div className="flex min-w-0 flex-1 flex-col">
      <header className="flex items-center justify-between border-b border-border px-4 py-3">
        <div className="min-w-0">
          <div className="text-xs uppercase tracking-wide text-muted-foreground">Sala</div>
          <div className="truncate text-sm font-medium">{roomId}</div>
        </div>
        <button
          onClick={copyLink}
          className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
        >
          {copied ? "Link copiado!" : "Copiar link"}
        </button>
      </header>

      <main className="flex-1 overflow-auto p-3">
        <div className={`grid gap-3 ${cols}`}>
          {tiles.map((t) => (
            <Tile key={t.peerId} label={t.label}>
              <VideoEl stream={t.stream} muted={t.local} />
            </Tile>
          ))}
        </div>
      </main>

      <footer className="flex items-center justify-center gap-3 border-t border-border px-4 py-4">
        <CtrlBtn active={micOn} onClick={toggleMic} label={micOn ? "Mic" : "Mic off"} />
        <CtrlBtn active={camOn} onClick={toggleCam} label={camOn ? "Câmera" : "Câmera off"} />
        <CtrlBtn
          active={chatOpen}
          onClick={() => {
            setChatOpen((o) => {
              if (!o) chat.markRead();
              return !o;
            });
          }}
          label={chat.unread > 0 && !chatOpen ? `Chat (${chat.unread})` : "Chat"}
        />
        <button
          onClick={leave}
          className="rounded-full bg-red-600 px-6 py-2.5 text-sm font-medium text-white hover:bg-red-700"
        >
          Sair
        </button>
      </footer>
    </div>
      <ChatPanel
        open={chatOpen}
        onClose={() => setChatOpen(false)}
        messages={chat.messages}
        onSend={chat.send}
      />
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

function Tile({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="relative aspect-video overflow-hidden rounded-xl bg-black ring-1 ring-border">
      {children}
      <div className="absolute bottom-2 left-2 rounded bg-black/60 px-2 py-0.5 text-xs text-white">
        {label}
      </div>
    </div>
  );
}

function CtrlBtn({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-full px-4 py-2.5 text-sm font-medium transition ${
        active
          ? "bg-muted text-foreground hover:bg-muted/80"
          : "bg-red-600 text-white hover:bg-red-700"
      }`}
    >
      {label}
    </button>
  );
}

// Suppress unused import warning — RemoteTrack tipa eventos do SDK.
type _Keep = RemoteTrack;
