// Bridge postMessage entre este app (dentro do iframe) e o app parent (Teli).
// Contrato tipado, validação de origem, API simétrica.
//
// Uso no iframe (este app):
//   const bridge = createEmbedBridge({ allowedOrigins: [...] });
//   bridge.emit("ready", {});
//   bridge.on("mute", () => micOff());
//
// Uso no parent (Teli):
//   iframe.contentWindow.postMessage({ t: "mute" }, "https://echo-forge-rtc.lovable.app");
//   window.addEventListener("message", (e) => { if (e.data?.t === "joined") ... });

export type EmbedEvent =
  | { t: "ready" }
  | { t: "joined"; peerId: string; room: string }
  | { t: "peer-joined"; peerId: string }
  | { t: "peer-left"; peerId: string }
  | { t: "left" }
  | { t: "error"; message: string }
  | { t: "permission-denied"; kind: "camera" | "microphone" | "both" }
  | { t: "device-changed"; kind: "audio" | "video"; enabled: boolean };

export type EmbedCommand =
  | { t: "mute" }
  | { t: "unmute" }
  | { t: "camera-off" }
  | { t: "camera-on" }
  | { t: "leave" }
  | { t: "switch-device"; kind: "audio" | "video"; deviceId: string };

export interface EmbedBridgeOptions {
  /** Origens autorizadas a mandar comandos. Wildcard `*` só em dev. */
  allowedOrigins: (string | RegExp)[];
}

type Handler<T extends EmbedCommand["t"]> = (
  cmd: Extract<EmbedCommand, { t: T }>,
) => void;

export interface EmbedBridge {
  emit(event: EmbedEvent): void;
  on<T extends EmbedCommand["t"]>(type: T, handler: Handler<T>): () => void;
  destroy(): void;
}

function matchOrigin(origin: string, allow: (string | RegExp)[]): boolean {
  for (const rule of allow) {
    if (typeof rule === "string") {
      if (rule === "*" || rule === origin) return true;
    } else if (rule.test(origin)) {
      return true;
    }
  }
  return false;
}

export function createEmbedBridge(opts: EmbedBridgeOptions): EmbedBridge {
  const handlers = new Map<string, Set<(cmd: EmbedCommand) => void>>();

  const onMessage = (ev: MessageEvent) => {
    if (!matchOrigin(ev.origin, opts.allowedOrigins)) return;
    const data = ev.data as EmbedCommand | undefined;
    if (!data || typeof data.t !== "string") return;
    const set = handlers.get(data.t);
    if (!set) return;
    for (const fn of set) fn(data);
  };

  window.addEventListener("message", onMessage);

  return {
    emit(event) {
      if (window.parent && window.parent !== window) {
        // "*" aqui é aceitável: são eventos, não segredos; o parent
        // valida origem no seu próprio listener.
        window.parent.postMessage(event, "*");
      }
    },
    on(type, handler) {
      let set = handlers.get(type);
      if (!set) {
        set = new Set();
        handlers.set(type, set);
      }
      set.add(handler as (cmd: EmbedCommand) => void);
      return () => set!.delete(handler as (cmd: EmbedCommand) => void);
    },
    destroy() {
      window.removeEventListener("message", onMessage);
      handlers.clear();
    },
  };
}

/** Allowlist padrão pra origens Teli + localhost em dev. */
export const DEFAULT_ALLOWED_ORIGINS: (string | RegExp)[] = [
  /^https:\/\/([a-z0-9-]+\.)*teli\.app\.br$/i,
  /^https:\/\/([a-z0-9-]+\.)*lovable\.app$/i,
  /^http:\/\/localhost(:\d+)?$/,
];
