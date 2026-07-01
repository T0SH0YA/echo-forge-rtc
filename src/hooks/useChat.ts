import { useCallback, useEffect, useRef, useState } from "react";
import type { Room } from "../../sdk/src";

export interface ChatMessage {
  id: string;
  peerId: string;
  author: string;
  text: string;
  ts: number;
  self: boolean;
}

const CHAT_LABEL = "chat";

interface WirePayload {
  author: string;
  text: string;
  ts: number;
}

function decodePayload(payload: unknown): WirePayload | null {
  try {
    let raw: string;
    if (typeof payload === "string") {
      raw = payload;
    } else if (payload instanceof ArrayBuffer) {
      raw = new TextDecoder().decode(payload);
    } else if (ArrayBuffer.isView(payload)) {
      raw = new TextDecoder().decode(payload as ArrayBufferView);
    } else {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<WirePayload>;
    if (typeof parsed.text !== "string") return null;
    return {
      author: typeof parsed.author === "string" ? parsed.author : "convidado",
      text: parsed.text,
      ts: typeof parsed.ts === "number" ? parsed.ts : Date.now(),
    };
  } catch {
    return null;
  }
}

/**
 * Chat em tempo real da Teli sobre o data channel do Room.
 * Envia com room.broadcastData("chat", ...) e ouve room.on("data", ...).
 */
export function useChat(room: Room | null, selfName: string) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [unread, setUnread] = useState(0);
  const nameRef = useRef(selfName);
  nameRef.current = selfName;

  useEffect(() => {
    if (!room) return;

    const onData = (evt: { peerId: string; label: string; payload: unknown }) => {
      if (evt.label !== CHAT_LABEL) return;
      const msg = decodePayload(evt.payload);
      if (!msg) return;
      setMessages((prev) => [
        ...prev,
        {
          id: `${evt.peerId}-${msg.ts}-${Math.random().toString(36).slice(2, 8)}`,
          peerId: evt.peerId,
          author: msg.author,
          text: msg.text,
          ts: msg.ts,
          self: false,
        },
      ]);
      setUnread((n) => n + 1);
    };

    room.on("data", onData);
    return () => {
      const anyRoom = room as unknown as { off?: (e: string, fn: unknown) => void };
      anyRoom.off?.("data", onData);
    };
  }, [room]);

  const send = useCallback(
    (text: string) => {
      const trimmed = text.trim();
      if (!trimmed || !room) return;
      const ts = Date.now();
      const author = nameRef.current || "convidado";
      const wire: WirePayload = { author, text: trimmed, ts };
      try {
        room.broadcastData(CHAT_LABEL, JSON.stringify(wire));
      } catch {
        // canal ainda não pronto; ignora silenciosamente
      }
      setMessages((prev) => [
        ...prev,
        {
          id: `self-${ts}-${Math.random().toString(36).slice(2, 8)}`,
          peerId: "self",
          author,
          text: trimmed,
          ts,
          self: true,
        },
      ]);
    },
    [room],
  );

  const markRead = useCallback(() => setUnread(0), []);

  return { messages, send, unread, markRead };
}
