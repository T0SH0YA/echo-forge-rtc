import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { Mic, MicOff, Video, VideoOff, MessageSquare, PhoneOff, Copy, Check, MonitorUp, MonitorOff, Circle, FileText } from "lucide-react";
import { useChat } from "../hooks/useChat";
import { ChatPanel } from "../components/ChatPanel";
import { TranscriptPanel } from "../components/TranscriptPanel";
import { useActiveSpeaker } from "../hooks/useActiveSpeaker";
import { useScreenShare } from "../hooks/useScreenShare";
import { useRecorder } from "../hooks/useRecorder";
import { useTranscription } from "../hooks/useTranscription";
import { usePresence } from "../hooks/usePresence";
import { useAIOrganize } from "../hooks/useAIOrganize";
import { AIReportModal } from "../components/AIReportModal";
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
  const [transcriptOpen, setTranscriptOpen] = useState(false);
  const [copied, setCopied] = useState(false);

  const localVideoRef = useRef<HTMLVideoElement>(null);
  const roomRef = useRef<Room | null>(null);
  const localStreamRef = useRef<MediaStream | null>(null);

  const chat = useChat(room, name);
  const screen = useScreenShare(room);
  const recorder = useRecorder(localStreamRef.current, (blob, filename) => {
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
  const transcription = useTranscription(room, name || "Voce");
  const aiOrganize = useAIOrganize();
  const speakerSources = [
    { id: "local", stream: localStreamRef.current },
    ...remotes.map((r) => ({ id: r.peerId, stream: r.stream })),
  ];
  const activeSpeaker = useActiveSpeaker(speakerSources);

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
    // Dispara organização com IA se houver transcrição
    const fullText = transcription.getFullText();
    const participants = [name || "Você", ...remotes.map((r) => r.peerId)];
    if (transcription.lines.length > 0 && fullText.trim()) {
      void aiOrganize.organize({
        transcript: fullText,
        meetingTitle: `Sala ${roomId}`,
        participants,
      });
    }
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

  const aiModal = (
    <AIReportModal
      open={aiOrganize.open}
      onClose={aiOrganize.close}
      loading={aiOrganize.loading}
      error={aiOrganize.error}
      report={aiOrganize.report}
      meetingTitle={`Sala ${roomId}`}
    />
  );

  // ---------- LOBBY ----------
  if (phase === "lobby" || phase === "joining" || phase === "ended" || phase === "error") {
    return (
      <>
      {aiModal}
      <div className="flex min-h-[100dvh] items-center justify-center bg-background px-4 text-foreground">
        <div className="w-full max-w-md rounded-2xl border border-border/60 bg-card p-8 shadow-xl">
          <div className="mb-8 flex flex-col items-center gap-4 text-center">
            <img src={teliLogo} alt="Teli" className="h-14 w-auto" />
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
                className="w-full rounded-xl border border-border/60 bg-background px-4 py-3 text-sm outline-none transition focus:border-primary"
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
                  className="flex-1 rounded-xl border border-border/60 bg-background px-4 py-3 text-sm outline-none transition focus:border-primary"
                />
                <button
                  type="button"
                  onClick={() => setRoomId(makeRoomId())}
                  className="rounded-xl border border-border/60 px-4 text-xs font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground"
                  title="Gerar nova sala"
                >
                  Nova
                </button>
              </div>
            </label>

            <button
              onClick={join}
              disabled={phase === "joining" || !roomId.trim()}
              className="w-full rounded-xl bg-primary px-4 py-3.5 text-sm font-semibold text-primary-foreground transition hover:opacity-90 active:scale-[0.99] disabled:opacity-50"
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
      </>
    );
  }

  // ---------- IN-CALL ----------
  const tiles = [
      ...(screen.sharing && screen.screenStream
        ? [{ peerId: "screen", stream: screen.screenStream, label: "Sua tela", local: true }]
        : []),
    { peerId: "local", stream: localStreamRef.current, label: `${name} (você)`, local: true },
    ...remotes.map((r) => ({ peerId: r.peerId, stream: r.stream, label: r.peerId, local: false })),
  ];
  const cols = tiles.length === 1 ? "grid-cols-1" : tiles.length === 2 ? "grid-cols-1 sm:grid-cols-2" : "grid-cols-2 lg:grid-cols-3";

  return (
    <>
    {aiModal}
    <div className="flex h-[100dvh] bg-background text-foreground">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-between px-3 py-2.5 sm:px-5 sm:py-3">
          <div className="flex min-w-0 items-center gap-3">
            <img src={teliLogo} alt="Teli" className="h-6 w-auto sm:h-7" />
            <div className="truncate text-xs text-muted-foreground sm:text-sm">
              Sala <span className="font-medium text-foreground">{roomId}</span>
            </div>
          </div>
          <button
            onClick={copyLink}
            className="inline-flex items-center gap-1.5 rounded-lg border border-border/60 px-3 py-2 text-xs font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground"
          >
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            <span className="hidden sm:inline">{copied ? "Link copiado!" : "Copiar link"}</span>
          </button>
        </header>

        <main className="flex-1 overflow-auto p-3 sm:p-6">
          <div className={`grid gap-2 sm:gap-3 ${cols}`}>
            {tiles.map((t) => (
              <div
                key={t.peerId}
                className={
                  activeSpeaker === t.peerId
                    ? "rounded-xl ring-2 ring-primary ring-offset-2 ring-offset-background transition"
                    : "rounded-xl ring-0 transition"
                }
              >
                <Tile label={t.label}>
                  <VideoEl stream={t.stream} muted={t.local} />
                </Tile>
              </div>
            ))}
          </div>
        </main>

        <footer className="flex items-center justify-center gap-3 px-2 pb-6 pt-3 sm:gap-4">
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
            active={!screen.sharing}
            onClick={screen.toggle}
            label={screen.sharing ? "Parar apresentacao" : "Apresentar tela"}
            icon={screen.sharing ? <MonitorOff className="h-5 w-5" /> : <MonitorUp className="h-5 w-5" />}
          />
          <CtrlBtn
            active={!recorder.recording}
            onClick={recorder.toggle}
            label={recorder.recording ? "Parar gravacao" : "Gravar reuniao"}
            icon={<Circle className={recorder.recording ? "h-5 w-5 fill-current text-red-400" : "h-5 w-5"} />}
          />
          <CtrlBtn
            active={!transcriptOpen}
            onClick={() => setTranscriptOpen((o) => !o)}
            label="Transcricao"
            icon={<FileText className="h-5 w-5" />}
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
            className="ml-1 inline-flex h-12 w-14 items-center justify-center rounded-full bg-red-600 text-white transition hover:bg-red-700 sm:w-16"
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

      {transcriptOpen && (
        <>
          <div
            className="fixed inset-0 z-40 bg-black/40 sm:hidden"
            onClick={() => setTranscriptOpen(false)}
          />
          <div className="fixed inset-y-0 right-0 z-50 w-full max-w-sm sm:static sm:z-auto sm:max-w-none">
            <TranscriptPanel
              open={transcriptOpen}
              onClose={() => setTranscriptOpen(false)}
              lines={transcription.lines}
              active={transcription.active}
              supported={transcription.supported}
              onToggle={transcription.toggle}
              onClear={transcription.clear}
              getFullText={transcription.getFullText}
              organizing={aiOrganize.loading}
              onOrganize={() =>
                aiOrganize.organize({
                  transcript: transcription.getFullText(),
                  meetingTitle: `Sala ${roomId}`,
                  participants: [name || "Você", ...remotes.map((r) => r.peerId)],
                })
              }
            />
          </div>
        </>
      )}
    </div>
    </>
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
    <div className="group relative aspect-video overflow-hidden rounded-xl bg-muted ring-1 ring-border/60 transition duration-200 hover:ring-primary/50">
      {children}
      <div className="absolute bottom-2 left-2 flex items-center gap-1.5 rounded-md bg-black/60 px-2 py-1 text-xs font-medium text-white">
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
      className={`relative inline-flex h-12 w-12 items-center justify-center rounded-full transition duration-200 active:scale-95 ${
        active
          ? "bg-muted text-foreground hover:bg-muted/70"
          : "bg-red-500 text-white hover:bg-red-600"
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
