import { useCallback, useEffect, useRef, useState } from "react";
import type { Room } from "../../sdk/src";

export interface TranscriptLine {
  id: string;
  speaker: string;
  text: string;
  ts: number;
  self: boolean;
}

const TRANSCRIPT_LABEL = "transcript";

interface WireLine {
  speaker: string;
  text: string;
  ts: number;
}

function decodeWire(payload: unknown): WireLine | null {
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
    const parsed = JSON.parse(raw);
    if (typeof parsed.text !== "string" || !parsed.text) return null;
    return {
      speaker: typeof parsed.speaker === "string" ? parsed.speaker : "Participante",
      text: parsed.text,
      ts: typeof parsed.ts === "number" ? parsed.ts : Date.now(),
    };
  } catch {
    return null;
  }
}

function insertSorted(prev: TranscriptLine[], line: TranscriptLine): TranscriptLine[] {
  if (prev.some((l) => l.id === line.id)) return prev;
  const next = [...prev, line];
  next.sort((a, b) => a.ts - b.ts);
  return next;
}

/**
 * Transcricao automatica multi-participante da Teli.
 *
 * Como funciona (igual Meet/Zoom, mas 100% no cliente, sem API key):
 * - Cada participante transcreve o PROPRIO microfone com a Web Speech API nativa
 *   (o navegador nao da acesso ao audio remoto — por isso cada um transcreve o seu).
 * - Cada linha final e transmitida via room.broadcastData("transcript", ...).
 * - Todos os clientes recebem via room.on("data", ...) e montam UM UNICO documento
 *   ordenado por horario, com o nome de quem falou. Resultado: transcricao unificada.
 *
 * A organizacao/resumo por IA (ex.: GPT-4o-mini) exige um backend com a SUA chave —
 * exponha um endpoint e chame-o com o texto de getFullText(). Nunca coloque a chave no front.
 */
export function useTranscription(
  room: Room | null,
  speaker: string,
  lang = "pt-BR",
) {
  const [lines, setLines] = useState<TranscriptLine[]>([]);
  const [active, setActive] = useState(false);
  const [supported, setSupported] = useState(true);
  const recRef = useRef<any>(null);
  const wantOnRef = useRef(false);
  const speakerRef = useRef(speaker);
  const roomRef = useRef<Room | null>(room);

  useEffect(() => {
    speakerRef.current = speaker;
  }, [speaker]);
  useEffect(() => {
    roomRef.current = room;
  }, [room]);

  // Recebe as linhas dos OUTROS participantes e junta no documento unico.
  useEffect(() => {
    if (!room) return;
    const onData = (evt: { peerId: string; label: string; payload: unknown }) => {
      if (evt.label !== TRANSCRIPT_LABEL) return;
      const wire = decodeWire(evt.payload);
      if (!wire) return;
      setLines((prev) =>
        insertSorted(prev, {
          id: `${evt.peerId}-${wire.ts}-${wire.text.length}`,
          speaker: wire.speaker,
          text: wire.text,
          ts: wire.ts,
          self: false,
        }),
      );
    };
    room.on("data", onData);
    return () => {
      const anyRoom = room as unknown as { off?: (e: string, cb: unknown) => void };
      anyRoom.off?.("data", onData);
    };
  }, [room]);

  // Reconhecimento de fala do microfone local.
  useEffect(() => {
    const SR =
      (typeof window !== "undefined" &&
        ((window as any).SpeechRecognition || (window as any).webkitSpeechRecognition)) ||
      null;
    if (!SR) {
      setSupported(false);
      return;
    }
    const rec = new SR();
    rec.lang = lang;
    rec.continuous = true;
    rec.interimResults = false;

    rec.onresult = (e: any) => {
      for (let i = e.resultIndex; i < e.results.length; i++) {
        const res = e.results[i];
        if (res.isFinal) {
          const text = String(res[0].transcript || "").trim();
          if (text) {
            const ts = Date.now();
            const who = speakerRef.current || "Você";
            setLines((prev) =>
              insertSorted(prev, {
                id: `self-${ts}-${text.length}`,
                speaker: who,
                text,
                ts,
                self: true,
              }),
            );
            try {
              roomRef.current?.broadcastData(
                TRANSCRIPT_LABEL,
                JSON.stringify({ speaker: who, text, ts }),
              );
            } catch {
              // canal ainda nao pronto; ignora
            }
          }
        }
      }
    };
    rec.onend = () => {
      if (wantOnRef.current) {
        try {
          rec.start();
        } catch {
          // "already started"
        }
      } else {
        setActive(false);
      }
    };
    rec.onerror = () => {
      // no-speech / aborted — deixa o onend reiniciar
    };
    recRef.current = rec;

    return () => {
      wantOnRef.current = false;
      try {
        rec.stop();
      } catch {
        // ignore
      }
      recRef.current = null;
    };
  }, [lang]);

  const start = useCallback(() => {
    const rec = recRef.current;
    if (!rec) return;
    wantOnRef.current = true;
    try {
      rec.start();
      setActive(true);
    } catch {
      // ja iniciado
    }
  }, []);

  const stop = useCallback(() => {
    wantOnRef.current = false;
    const rec = recRef.current;
    if (rec) {
      try {
        rec.stop();
      } catch {
        // ignore
      }
    }
    setActive(false);
  }, []);

  const toggle = useCallback(() => {
    if (active) stop();
    else start();
  }, [active, start, stop]);

  const clear = useCallback(() => setLines([]), []);

  const getFullText = useCallback(() => {
    return lines
      .map((l) => {
        const t = new Date(l.ts).toLocaleTimeString([], {
          hour: "2-digit",
          minute: "2-digit",
        });
        return `[${t}] ${l.speaker}: ${l.text}`;
      })
      .join("\n");
  }, [lines]);

  return { lines, active, supported, start, stop, toggle, clear, getFullText };
}
