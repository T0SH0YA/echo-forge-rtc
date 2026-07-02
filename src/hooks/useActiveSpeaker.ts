import { useEffect, useRef, useState } from "react";

/**
 * Detecta quem esta falando analisando o volume (RMS) de cada faixa de audio.
 * Totalmente client-side, usando a Web Audio API — sem servidor, sem API key.
 *
 * @param sources lista de { id, stream } (local + remotos)
 * @returns o id do participante que esta falando no momento (ou null)
 */
export function useActiveSpeaker(
  sources: { id: string; stream: MediaStream | null }[],
): string | null {
  const [activeId, setActiveId] = useState<string | null>(null);
  const ctxRef = useRef<AudioContext | null>(null);
  const analysersRef = useRef<Map<string, { analyser: AnalyserNode; data: Uint8Array }>>(
    new Map(),
  );
  const rafRef = useRef<number | null>(null);

  const key = sources
    .map((s) => `${s.id}:${s.stream ? s.stream.id : "none"}`)
    .join("|");

  useEffect(() => {
    if (typeof window === "undefined") return;
    const AC = window.AudioContext || (window as any).webkitAudioContext;
    if (!AC) return;
    if (!ctxRef.current) ctxRef.current = new AC();
    const ctx = ctxRef.current;

    const map = analysersRef.current;
    const seen = new Set<string>();
    const nodes: { id: string; src: MediaStreamAudioSourceNode }[] = [];

    for (const s of sources) {
      if (!s.stream) continue;
      if (s.stream.getAudioTracks().length === 0) continue;
      seen.add(s.id);
      if (map.has(s.id)) continue;
      try {
        const src = ctx.createMediaStreamSource(s.stream);
        const analyser = ctx.createAnalyser();
        analyser.fftSize = 512;
        analyser.smoothingTimeConstant = 0.6;
        src.connect(analyser);
        map.set(s.id, { analyser, data: new Uint8Array(analyser.frequencyBinCount) });
        nodes.push({ id: s.id, src });
      } catch {
        // ignora streams que nao podem ser conectados
      }
    }

    for (const id of [...map.keys()]) {
      if (!seen.has(id)) map.delete(id);
    }

    let lastSwitch = 0;
    const THRESHOLD = 18;

    const tick = () => {
      const m = analysersRef.current;
      let loudestId: string | null = null;
      let loudest = 0;
      for (const [id, { analyser, data }] of m) {
        analyser.getByteFrequencyData(data as Uint8Array<ArrayBuffer>);
        let sum = 0;
        for (let i = 0; i < data.length; i++) sum += data[i];
        const avg = sum / data.length;
        if (avg > loudest) {
          loudest = avg;
          loudestId = id;
        }
      }
      const now = performance.now();
      if (loudest >= THRESHOLD && now - lastSwitch > 350) {
        setActiveId((prev) => {
          if (prev !== loudestId) lastSwitch = now;
          return loudestId;
        });
      } else if (loudest < THRESHOLD && now - lastSwitch > 1200) {
        setActiveId((prev) => (prev !== null ? null : prev));
      }
      rafRef.current = requestAnimationFrame(tick);
    };
    rafRef.current = requestAnimationFrame(tick);

    return () => {
      if (rafRef.current) cancelAnimationFrame(rafRef.current);
      for (const { src } of nodes) {
        try {
          src.disconnect();
        } catch {
          // ignore
        }
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  return activeId;
}
