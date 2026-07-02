import { useEffect, useRef, useState } from "react";
import type { Room } from "../../sdk/src";

const LABEL = "presence";

/**
 * Presence: cada participante anuncia seu nome via data channel.
 * - Ao entrar, broadcast do proprio nome.
 * - Quando um novo peer entra, re-broadcast (senao o novo nao sabe quem somos).
 * - Ao receber, guarda o mapa peerId -> name.
 */
export function usePresence(room: Room | null, selfName: string) {
  const [names, setNames] = useState<Record<string, string>>({});
  const nameRef = useRef(selfName);
  nameRef.current = selfName;

  useEffect(() => {
    if (!room) return;

    const announce = () => {
      try {
        room.broadcastData(LABEL, JSON.stringify({ name: nameRef.current || "convidado" }));
      } catch {
        // canal ainda nao pronto — sera reenviado no peer-joined
      }
    };

    const onData = (evt: { peerId: string; label: string; payload: unknown }) => {
      if (evt.label !== LABEL) return;
      try {
        let raw: string;
        const p = evt.payload;
        if (typeof p === "string") raw = p;
        else if (p instanceof ArrayBuffer) raw = new TextDecoder().decode(p);
        else if (ArrayBuffer.isView(p)) raw = new TextDecoder().decode(p as ArrayBufferView);
        else return;
        const parsed = JSON.parse(raw) as { name?: string };
        if (typeof parsed.name === "string" && parsed.name.trim()) {
          setNames((n) => ({ ...n, [evt.peerId]: parsed.name!.trim() }));
        }
      } catch {
        // ignore
      }
    };

    const onPeerJoined = () => {
      // reenvia com pequeno atraso (data channel abre apos negociacao)
      setTimeout(announce, 500);
      setTimeout(announce, 2000);
    };

    room.on("data", onData);
    room.on("peer-joined", onPeerJoined);

    // primeiro anuncio (para peers ja presentes)
    setTimeout(announce, 500);
    setTimeout(announce, 2000);

    return () => {
      const anyRoom = room as unknown as { off?: (e: string, cb: unknown) => void };
      anyRoom.off?.("data", onData);
      anyRoom.off?.("peer-joined", onPeerJoined);
    };
  }, [room]);

  // Reanuncia se o usuario mudar o proprio nome
  useEffect(() => {
    if (!room) return;
    try {
      room.broadcastData(LABEL, JSON.stringify({ name: selfName || "convidado" }));
    } catch {
      // ignore
    }
  }, [room, selfName]);

  return names;
}
