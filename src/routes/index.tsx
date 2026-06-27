import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";

export const Route = createFileRoute("/")({
  head: () => ({
    meta: [
      { title: "WebRTC próprio — Playground" },
      { name: "description", content: "Bancada de teste do SDK WebRTC próprio. Aponta para sinalização, STUN, TURN e SFU desenvolvidos do zero." },
    ],
  }),
  component: Playground,
});

function Playground() {
  const [signalingUrl, setSignalingUrl] = useState("wss://localhost:8080");
  const [roomId, setRoomId] = useState("demo");
  const [token, setToken] = useState("");
  const [status, setStatus] = useState<"idle" | "connecting" | "connected" | "error">("idle");
  const [log, setLog] = useState<string[]>([]);

  const append = (line: string) => setLog((l) => [...l, `${new Date().toISOString().slice(11, 19)}  ${line}`]);

  const connect = () => {
    setStatus("connecting");
    append(`Etapa 2 ainda não implementada — esta UI é o esqueleto.`);
    append(`Quando a Etapa 2 rodar: WS → ${signalingUrl}/v1/rooms/${roomId}?token=...`);
    setStatus("idle");
  };

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b border-border">
        <div className="mx-auto max-w-5xl px-6 py-5">
          <h1 className="text-2xl font-semibold tracking-tight">WebRTC próprio · Playground</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Bancada de teste. Sinalização, STUN, TURN, SFU e SDK construídos do zero. Etapa 1 (esqueleto) ativa.
          </p>
        </div>
      </header>

      <main className="mx-auto max-w-5xl px-6 py-8 space-y-8">
        <section className="grid gap-4 rounded-lg border border-border p-5 sm:grid-cols-3">
          <Field label="Signaling URL" value={signalingUrl} onChange={setSignalingUrl} placeholder="wss://host:8080" />
          <Field label="Room ID" value={roomId} onChange={setRoomId} placeholder="demo" />
          <Field label="JWT token" value={token} onChange={setToken} placeholder="<jwt>" />
          <div className="sm:col-span-3 flex items-center gap-3">
            <button
              onClick={connect}
              className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
            >
              Conectar
            </button>
            <StatusPill status={status} />
          </div>
        </section>

        <section className="grid gap-4 sm:grid-cols-2">
          <VideoTile label="Local" />
          <VideoTile label="Remoto" />
        </section>

        <section className="rounded-lg border border-border">
          <div className="border-b border-border px-4 py-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Log
          </div>
          <pre className="max-h-64 overflow-auto px-4 py-3 text-xs leading-relaxed">
            {log.length === 0 ? <span className="text-muted-foreground">Sem eventos.</span> : log.join("\n")}
          </pre>
        </section>

        <section className="rounded-lg border border-border bg-muted/30 p-5 text-sm">
          <h2 className="font-medium">Onde está cada peça</h2>
          <ul className="mt-2 space-y-1 text-muted-foreground">
            <li><code className="text-foreground">/sdk</code> — SDK TypeScript</li>
            <li><code className="text-foreground">/server/signaling</code> — WebSocket Go</li>
            <li><code className="text-foreground">/server/stun</code> — RFC 5389 do zero</li>
            <li><code className="text-foreground">/server/turn</code> — RFC 5766/8656 do zero</li>
            <li><code className="text-foreground">/server/sfu</code> — ICE/DTLS/SRTP em Go</li>
            <li><code className="text-foreground">/infra</code> — Docker + deploy</li>
            <li><code className="text-foreground">/docs/protocol</code> — specs do protocolo</li>
          </ul>
        </section>
      </main>
    </div>
  );
}

function Field({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
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

function VideoTile({ label }: { label: string }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-muted/40">
      <div className="flex items-center justify-between border-b border-border px-3 py-2 text-xs uppercase tracking-wide text-muted-foreground">
        <span>{label}</span>
        <span className="text-[10px]">aguardando Etapa 2</span>
      </div>
      <div className="aspect-video w-full bg-black/80" />
    </div>
  );
}

function StatusPill({ status }: { status: "idle" | "connecting" | "connected" | "error" }) {
  const map: Record<typeof status, string> = {
    idle: "bg-muted text-muted-foreground",
    connecting: "bg-yellow-500/20 text-yellow-700 dark:text-yellow-300",
    connected: "bg-green-500/20 text-green-700 dark:text-green-300",
    error: "bg-red-500/20 text-red-700 dark:text-red-300",
  };
  return <span className={`rounded-full px-2 py-0.5 text-xs ${map[status]}`}>{status}</span>;
}
