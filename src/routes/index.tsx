import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { Mic, MicOff, Video, VideoOff, MessageSquare, PhoneOff, Copy, Check } from "lucide-react";
import { useChat } from "../hooks/useChat";
import { ChatPanel } from "../components/ChatPanel";
import teliLogoAsset from "../assets/teli-logo.png.asset.json";
const teliLogo = teliLogoAsset.url;
import { Client, type Room, type RemoteTrack } from "../../sdk/src";

export const Route = createFileRoute("/")({
  head: () => ({
    meta: [
      { title: "Teli — Vídeo e chat em tempo real" },
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
      // 1. pega câmera/mic — se não houver câmera, cai pra só áudio
      let stream: MediaStream;
      let hasVideo = true;
      try {
        stream = await navigator.mediaDevices.getUserMedia({ video: true, audio: true });
      } catch (err) {
        const name = (err as DOMException)?.name;
        if (name === "NotFoundError" || name === "OverconstrainedError" || name === "NotReadableError") {
          // sem câmera disponível — tenta só áudio
          try {
            stream = await navigator.mediaDevices.getUserMedia({ video: false, audio: true });
            hasVideo = false;
          } catch (err2) {
            throw new Error(
              "Nenhum microfone/câmera encontrados. Verifique se algum dispositivo está conectado e se o navegador tem permissão.",
            );
          }
        } else if (name === "NotAllowedError") {
          throw new Error("Permissão de câmera/microfone negada. Libere nas configurações do navegador.");
        } else {
          throw err;
        }
      }
      localStreamRef.current = stream;
      if (!hasVideo) setCamOn(false);
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

      // 3. publica as tracks do stream JÁ capturado (para que toggleMic/Cam funcione)
      for (const track of stream.getTracks()) {
        await room.publishTrack(track);
      }

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
          <div className="mb-6 flex flex-col items-center gap-3 text-center">
            <img src={teliLogo} alt="Teli" className="h-32 w-auto" />
            <h1 className="text-2xl font-semibold tracking-tight">Sua reunião começa aqui</h1>
          </div>


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
    <div className="flex h-[100dvh] bg-background text-foreground">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-between border-b border-border px-3 py-2 sm:px-4 sm:py-3">
          <div className="flex min-w-0 items-center gap-3">
            <img src={teliLogo} alt="Teli" className="h-6 w-auto sm:h-7" />
            <div className="truncate text-xs text-muted-foreground sm:text-sm">
              Sala <span className="font-medium text-foreground">{roomId}</span>
            </div>
          </div>
          <button
            onClick={copyLink}
            className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs hover:bg-muted"
          >
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            <span className="hidden sm:inline">{copied ? "Link copiado!" : "Copiar link"}</span>
          </button>
        </header>

        <main className="flex-1 overflow-auto p-2 sm:p-3">
          <div className={`grid gap-2 sm:gap-3 ${cols}`}>
            {tiles.map((t) => (
              <Tile key={t.peerId} label={t.label}>
                <VideoEl stream={t.stream} muted={t.local} />
              </Tile>
            ))}
          </div>
        </main>

        <footer className="flex items-center justify-center gap-2 border-t border-border px-2 py-3 sm:gap-3 sm:py-4">
          <CtrlBtn
            active={micOn}
            onClick={toggleMic}
            label={micOn ? "Microfone ligado" : "Microfone desligado"}
            icon={micOn ? <Mic className="h-5 w-5" /> : <MicOff className="h-5 w-5" />}
          />
          <CtrlBtn
            active={camOn}
            onClick={toggleCam}
            label={camOn ? "Câmera ligada" : "Câmera desligada"}
            icon={camOn ? <Video className="h-5 w-5" /> : <VideoOff className="h-5 w-5" />}
          />
          <CtrlBtn
            active={!chatOpen}
            onClick={() => {
              setChatOpen((o) => {
                if (!o) chat.markRead();
                return !o;
              });
            }}
            label="Chat"
            icon={<MessageSquare className="h-5 w-5" />}
            badge={chat.unread > 0 && !chatOpen ? chat.unread : undefined}
          />
          <button
            onClick={leave}
            aria-label="Sair da chamada"
            className="ml-1 inline-flex h-12 w-14 items-center justify-center rounded-full bg-red-600 text-white shadow-sm transition hover:bg-red-700 sm:w-16"
          >
            <PhoneOff className="h-5 w-5" />
          </button>
        </footer>
      </div>

      {/* Chat: overlay em mobile, sidebar em desktop */}
      {chatOpen && (
        <>
          <div
            className="fixed inset-0 z-40 bg-black/40 sm:hidden"
            onClick={() => setChatOpen(false)}
          />
          <div className="fixed inset-y-0 right-0 z-50 w-full max-w-sm sm:static sm:z-auto sm:max-w-none">
            <ChatPanel
              open={chatOpen}
              onClose={() => setChatOpen(false)}
              messages={chat.messages}
              onSend={chat.send}
            />
          </div>
        </>
      )}
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

function CtrlBtn({
  active,
  onClick,
  label,
  icon,
  badge,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  icon: React.ReactNode;
  badge?: number;
}) {
  return (
    <button
      onClick={onClick}
      aria-label={label}
      title={label}
      className={`relative inline-flex h-12 w-12 items-center justify-center rounded-full transition sm:h-12 sm:w-12 ${
        active
          ? "bg-muted text-foreground hover:bg-muted/80"
          : "bg-red-600 text-white hover:bg-red-700"
      }`}
    >
      {icon}
      {badge ? (
        <span className="absolute -right-1 -top-1 flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-semibold text-primary-foreground">
          {badge}
        </span>
      ) : null}
    </button>
  );
}

// Suppress unused import warning — RemoteTrack tipa eventos do SDK.
type _Keep = RemoteTrack;
