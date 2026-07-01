import { useCallback, useEffect, useRef, useState } from "react";

export interface TranscriptLine {
  id: string;
  speaker: string;
  text: string;
  ts: number;
}

/**
 * Transcricao automatica usando a Web Speech API nativa do navegador.
 *
 * Observacoes importantes:
 * - Transcreve o MICROFONE LOCAL (a Web Speech API nao acessa o audio remoto).
 *   Para uma reuniao, cada participante transcreve a propria fala; junte depois se quiser.
 * - Nao usa nenhuma API key e nao envia audio para servidores externos.
 * - Suporte varia por navegador (melhor no Chrome). pt-BR por padrao.
 *
 * A organizacao/resumo por IA (ex.: GPT-4o-mini) exige um backend com a SUA chave —
 * exponha um endpoint e chame-o com o texto de getFullText(). Nunca coloque a chave no front.
 */
export function useTranscription(speaker: string, lang = "pt-BR") {
  const [lines, setLines] = useState<TranscriptLine[]>([]);
  const [active, setActive] = useState(false);
  const [supported, setSupported] = useState(true);
  const recRef = useRef<any>(null);
  const wantOnRef = useRef(false);

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
          const text = (res[0]?.transcript || "").trim();
          if (text) {
            setLines((prev) => [
              ...prev,
              {
                id: `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
                speaker,
                text,
                ts: Date.now(),
              },
            ]);
          }
        }
      }
    };
    rec.onend = () => {
      // o navegador encerra sozinho apos silencio; reinicia se ainda quisermos ouvir
      if (wantOnRef.current) {
        try {
          rec.start();
        } catch {
          // ignora "already started"
        }
      } else {
        setActive(false);
      }
    };
    rec.onerror = () => {
      // erros comuns: no-speech, aborted — deixa o onend cuidar do reinicio
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
  }, [lang, speaker]);

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
    const rec = recRef.current;
    wantOnRef.current = false;
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
