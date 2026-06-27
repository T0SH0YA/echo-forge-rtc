import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { Client, type Room, type RemoteTrack } from "../../sdk/src";

export const Route = createFileRoute("/")({
  head: () => ({
    meta: [
      { title: "WebRTC próprio — Playground" },
      {
        name: "description",
        content:
          "Bancada de teste do SDK WebRTC próprio. Etapa 2: P2P em malha com sinalização (WebSocket ou loopback BroadcastChannel).",
      },
    ],
  }),
  component: Playground,
});

type Status = "idle" | "connecting" | "connected" | "publishing" | "error" | "closed";

interface RemoteEntry {
  peerId: string;
  stream: MediaStream;
}

function Playground() {
  const [signalingUrl, setSignalingUrl] = useState("bc://lovable-loopback");
  const [roomId, setRoomId] = useState("demo");
  const [token, setToken] = useState("dev");
  const [status, setStatus] = useState<Status>("idle");
  const [log, setLog] = useState<string[]>([]);
  const [remotes, setRemotes] = useState<RemoteEntry[]>([]);
  const localVideoRef = useRef<HTMLVideoElement>(null);
  const roomRef = useRef<Room | null>(null);

  const append = (line: string) =>
    setLog((l) => [...l, `${new Date().toISOString().slice(11, 19)}  ${line}`].slice(-200));

  useEffect(() => {
    return () => {
      void roomRef.current?.leave();
    };
  }, []);

  const connect = async () => {
    if (roomRef.current) {
      append("já conectado — desconecte antes");
      return;
    }
    setStatus("connecting");
    try {
      const client = new Client({ url: signalingUrl });
      const room = await client.join({ roomId, token });
      roomRef.current = room;
      append(`welcome · peerId=${room.localPeerId}`);
      setStatus("connected");

      room.on("peer-joined", (p) => append(`peer-joined · ${p.id}`));
      room.on("peer-left", ({ peerId }) => {
        append(`peer-left · ${peerId}`);
        setRemotes((r) => r.filter((x) => x.peerId !== peerId));
      });
      room.on("track-subscribed", ({ peer, track, stream }) => {
        append(`track · ${peer.id} ${track.kind}`);
        setRemotes((r) => {
          const existing = r.find((x) => x.peerId === peer.id);
          if (existing) {
            if (!existing.stream.getTracks().includes(track.mediaStreamTrack)) {
              existing.stream.addTrack(track.mediaStreamTrack);
            }
            return [...r];
          }
          return [...r, { peerId: peer.id, stream }];
        });
      });
      room.on("connection-state", (s) => append(`state · ${s}`));
      room.on("error", (e) => append(`error · ${e.message}`));
    } catch (err) {
      setStatus("error");
      append(`falha · ${(err as Error).message}`);
    }
  };

  const publish = async () => {
    const room = roomRef.current;
    if (!room) {
      append("conecte primeiro");
      return;
    }
    setStatus("publishing");
    try {
      const bundle = await room.publishCamera({ video: true, audio: true });
      if (localVideoRef.current) {
        localVideoRef.current.srcObject = bundle.stream;
        await localVideoRef.current.play().catch(() => {});
      }
      append(`publicou ${bundle.stream.getTracks().map((t) => t.kind).join("+")}`);
      setStatus("connected");
    } catch (err) {
      setStatus("error");
      append(`getUserMedia falhou · ${(err as Error).message}`);
    }
  };

  const leave = async () => {
    await roomRef.current?.leave();
    roomRef.current = null;
    setRemotes([]);
    setStatus("closed");
    if (localVideoRef.current) localVideoRef.current.srcObject = null;
    append("saiu");
  };

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b border-border">
        <div className="mx-auto max-w-6xl px-6 py-5">
          <h1 className="text-2xl font-semibold tracking-tight">WebRTC próprio · Playground</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Etapa 2 ativa · P2P em malha · transporte <code>bc://</code> (loopback entre abas) ou{" "}
            <code>wss://</code> (servidor Go).
          </p>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-6 py-8 space-y-6">
        <section className="grid gap-4 rounded-lg border border-border p-5 sm:grid-cols-3">
          <Field
            label="Signaling URL"
            value={signalingUrl}
            onChange={setSignalingUrl}
            placeholder="bc://lovable-loopback ou wss://host:8080"
          />
          <Field label="Room ID" value={roomId} onChange={setRoomId} placeholder="demo" />
          <Field label="Token" value={token} onChange={setToken} placeholder="<jwt>" />
          <div className="sm:col-span-3 flex flex-wrap items-center gap-3">
            <button
              onClick={connect}
              disabled={status === "connecting" || status === "connected" || status === "publishing"}
              className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              Conectar
            </button>
            <button
              onClick={publish}
              disabled={status !== "connected"}
              className="rounded-md border border-border px-4 py-2 text-sm font-medium hover:bg-muted disabled:opacity-50"
            >
              Publicar câmera
            </button>
            <button
              onClick={leave}
              disabled={!roomRef.current}
              className="rounded-md border border-border px-4 py-2 text-sm font-medium hover:bg-muted disabled:opacity-50"
            >
              Sair
            </button>
            <StatusPill status={status} />
            <span className="ml-auto text-xs text-muted-foreground">
              Dica: abra esta mesma URL em outra aba pra testar mesh com <code>bc://</code>.
            </span>
          </div>
        </section>

        <section className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Tile label="Local (você)">
            <video ref={localVideoRef} autoPlay playsInline muted className="h-full w-full object-cover" />
          </Tile>
          {remotes.map((r) => (
            <Tile key={r.peerId} label={r.peerId}>
              <RemoteVideo stream={r.stream} />
            </Tile>
          ))}
          {remotes.length === 0 && (
            <Tile label="Remoto" muted>
              <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
                sem peers ainda
              </div>
            </Tile>
          )}
        </section>

        <section className="rounded-lg border border-border">
          <div className="border-b border-border px-4 py-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Log
          </div>
          <pre className="max-h-72 overflow-auto px-4 py-3 text-xs leading-relaxed">
            {log.length === 0 ? <span className="text-muted-foreground">Sem eventos.</span> : log.join("\n")}
          </pre>
        </section>
      </main>
    </div>
  );
}

function RemoteVideo({ stream }: { stream: MediaStream }) {
  const ref = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    if (ref.current) {
      ref.current.srcObject = stream;
      void ref.current.play().catch(() => {});
    }
  }, [stream]);
  return <video ref={ref} autoPlay playsInline className="h-full w-full object-cover" />;
}

function Field({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</span>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
      />
    </label>
  );
}

function Tile({ label, children, muted }: { label: string; children: React.ReactNode; muted?: boolean }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-muted/40">
      <div className="flex items-center justify-between border-b border-border px-3 py-2 text-xs uppercase tracking-wide text-muted-foreground">
        <span className="truncate">{label}</span>
        {muted && <span className="text-[10px]">placeholder</span>}
      </div>
      <div className="aspect-video w-full bg-black/80">{children}</div>
    </div>
  );
}

function StatusPill({ status }: { status: Status }) {
  const map: Record<Status, string> = {
    idle: "bg-muted text-muted-foreground",
    connecting: "bg-yellow-500/20 text-yellow-700 dark:text-yellow-300",
    connected: "bg-green-500/20 text-green-700 dark:text-green-300",
    publishing: "bg-blue-500/20 text-blue-700 dark:text-blue-300",
    error: "bg-red-500/20 text-red-700 dark:text-red-300",
    closed: "bg-muted text-muted-foreground",
  };
  return <span className={`rounded-full px-2 py-0.5 text-xs ${map[status]}`}>{status}</span>;
}
