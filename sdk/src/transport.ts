// Transporte de sinalização. Abstrai WebSocket pra permitir testes via
// BroadcastChannel (duas abas do mesmo browser conversando sem servidor).
// URL scheme:
//   ws://, wss://     -> WebSocket real contra nosso servidor de sinalização
//   bc://<channel>    -> BroadcastChannel local (apenas dev/teste no browser)
import type { SignalingIn, SignalingOut } from "./types";

export interface SignalingTransport {
  send(msg: SignalingOut): void;
  onMessage(fn: (msg: SignalingIn) => void): void;
  onClose(fn: () => void): void;
  close(): void;
}

export async function openTransport(url: string, roomId: string, token: string): Promise<SignalingTransport> {
  if (url.startsWith("bc://")) {
    return openBroadcastChannel(url.slice(5), roomId);
  }
  return openWebSocket(url, roomId, token);
}

// ---------- WebSocket real ----------

function openWebSocket(url: string, roomId: string, token: string): Promise<SignalingTransport> {
  const wsUrl = `${url.replace(/\/$/, "")}/v1/rooms/${encodeURIComponent(roomId)}?token=${encodeURIComponent(token)}`;
  const ws = new WebSocket(wsUrl);

  return new Promise((resolve, reject) => {
    let opened = false;
    const messageHandlers = new Set<(m: SignalingIn) => void>();
    const closeHandlers = new Set<() => void>();

    ws.onopen = () => {
      opened = true;
      resolve({
        send: (msg) => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify(msg)),
        onMessage: (fn) => void messageHandlers.add(fn),
        onClose: (fn) => void closeHandlers.add(fn),
        close: () => ws.close(),
      });
    };
    ws.onmessage = (ev) => {
      let msg: SignalingIn;
      try {
        msg = JSON.parse(typeof ev.data === "string" ? ev.data : "");
      } catch {
        return;
      }
      for (const fn of messageHandlers) fn(msg);
    };
    ws.onerror = () => {
      if (!opened) reject(new Error("websocket connection failed"));
    };
    ws.onclose = () => {
      for (const fn of closeHandlers) fn();
    };
  });
}

// ---------- BroadcastChannel (loopback in-browser) ----------
//
// Emula o servidor de sinalização entre abas do mesmo origin.
// Cada peer publica um "hello" no canal, todos respondem com "presence",
// e mensagens addressed (`offer`/`answer`/`ice`) carregam `to` + `from`.
// Não usa servidor; só serve pra teste em duas abas dentro do Lovable preview.

interface BCFrame {
  channel: string; // roomId
  from: string;
  to?: string; // se ausente, broadcast
  payload: SignalingIn | SignalingOut | { t: "presence"; data: { peerId: string } };
}

function openBroadcastChannel(channelName: string, roomId: string): Promise<SignalingTransport> {
  if (typeof BroadcastChannel === "undefined") {
    return Promise.reject(new Error("BroadcastChannel não suportado neste ambiente"));
  }
  const bc = new BroadcastChannel(`webrtc-own:${channelName}:${roomId}`);
  const localPeerId = "p_" + Math.random().toString(36).slice(2, 10);
  const knownPeers = new Set<string>();

  const messageHandlers = new Set<(m: SignalingIn) => void>();
  const closeHandlers = new Set<() => void>();
  // Buffer pra mensagens emitidas antes do consumidor registrar onMessage.
  const pending: SignalingIn[] = [];

  bc.onmessage = (ev) => {
    const frame = ev.data as BCFrame;
    if (!frame || frame.from === localPeerId) return;
    if (frame.to && frame.to !== localPeerId) return;

    const payload = frame.payload;

    if (payload.t === "presence") {
      const otherId = payload.data.peerId;
      if (knownPeers.has(otherId)) return;
      knownPeers.add(otherId);
      emit({ t: "peer-join", data: { peer: { id: otherId } } });
      bc.postMessage({
        channel: roomId,
        from: localPeerId,
        to: otherId,
        payload: { t: "presence", data: { peerId: localPeerId } },
      } satisfies BCFrame);
      return;
    }

    if (payload.t === "offer" || payload.t === "answer" || payload.t === "ice") {
      const data = { ...(payload.data as Record<string, unknown>), from: frame.from };
      delete (data as { to?: string }).to;
      emit({ t: payload.t, data } as SignalingIn);
    }
  };

  function emit(msg: SignalingIn) {
    if (messageHandlers.size === 0) {
      pending.push(msg);
      return;
    }
    for (const fn of messageHandlers) fn(msg);
  }

  // Welcome local + anúncio de presença. Bufferizado se ninguém ouviu ainda.
  queueMicrotask(() => {
    emit({
      t: "welcome",
      data: { peerId: localPeerId, room: roomId, peers: [], iceServers: [] },
    });
    bc.postMessage({
      channel: roomId,
      from: localPeerId,
      payload: { t: "presence", data: { peerId: localPeerId } },
    } satisfies BCFrame);
  });

  const transport: SignalingTransport = {
    send: (msg) => {
      if (msg.t === "leave") {
        bc.close();
        for (const fn of closeHandlers) fn();
        return;
      }
      if (msg.t === "hello") return; // não há servidor pra responder; já emitimos welcome
      // mensagens addressed
      const to = (msg as { data?: { to?: string } }).data?.to;
      bc.postMessage({
        channel: roomId,
        from: localPeerId,
        to,
        payload: msg,
      } satisfies BCFrame);
    },
    onMessage: (fn) => {
      messageHandlers.add(fn);
      // drena o que chegou antes do handler ser registrado
      if (pending.length > 0) {
        const drain = pending.splice(0, pending.length);
        for (const m of drain) fn(m);
      }
    },
    onClose: (fn) => void closeHandlers.add(fn),
    close: () => {
      bc.close();
      for (const fn of closeHandlers) fn();
    },
  };

  return Promise.resolve(transport);
}
