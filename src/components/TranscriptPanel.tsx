import { useEffect, useRef } from "react";
import { X, Mic, Circle, Download, Sparkles } from "lucide-react";
import type { TranscriptLine } from "../hooks/useTranscription";

interface TranscriptPanelProps {
  open: boolean;
  onClose: () => void;
  lines: TranscriptLine[];
  active: boolean;
  supported: boolean;
  onToggle: () => void;
  onClear: () => void;
  getFullText: () => string;
  onOrganize?: () => void;
  organizing?: boolean;
}

function formatTime(ts: number): string {
  try {
    return new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  } catch {
    return "";
  }
}

export function TranscriptPanel({
  open,
  onClose,
  lines,
  active,
  supported,
  onToggle,
  onClear,
  getFullText,
  onOrganize,
  organizing,
}: TranscriptPanelProps) {
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (listRef.current) listRef.current.scrollTop = listRef.current.scrollHeight;
  }, [lines, open]);

  if (!open) return null;

  const downloadTxt = () => {
    const text = getFullText();
    if (!text) return;
    const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `teli-transcricao-${new Date().toISOString().slice(0, 10)}.txt`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  return (
    <aside className="flex h-full w-full flex-col bg-card sm:w-[360px] sm:rounded-2xl sm:border sm:border-border/60">
      <header className="flex items-center justify-between px-5 pb-3 pt-4">
        <h2 className="text-base font-semibold text-foreground">Transcrição</h2>
        <button
          type="button"
          onClick={onClose}
          aria-label="Fechar transcrição"
          className="flex h-8 w-8 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted"
        >
          <X className="h-[18px] w-[18px]" />
        </button>
      </header>

      <div className="flex flex-wrap items-center gap-2 px-5 pb-3">
        <button
          type="button"
          onClick={onToggle}
          disabled={!supported}
          className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs font-medium transition disabled:opacity-40 ${
            active
              ? "bg-red-500 text-white hover:bg-red-600"
              : "bg-primary text-primary-foreground hover:opacity-90"
          }`}
        >
          {active ? <Circle className="h-3 w-3 fill-current" /> : <Mic className="h-3.5 w-3.5" />}
          {active ? "Gravando" : "Transcrever"}
        </button>
        <button
          type="button"
          onClick={downloadTxt}
          disabled={lines.length === 0}
          className="inline-flex items-center gap-1.5 rounded-full border border-border/60 px-3 py-1.5 text-xs font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground disabled:opacity-40"
        >
          <Download className="h-3.5 w-3.5" />
          .txt
        </button>
        {onOrganize && (
          <button
            type="button"
            onClick={onOrganize}
            disabled={lines.length === 0 || organizing}
            className="inline-flex items-center gap-1.5 rounded-full bg-primary/10 px-3 py-1.5 text-xs font-medium text-primary transition hover:bg-primary/20 disabled:opacity-40"
          >
            <Sparkles className="h-3.5 w-3.5" />
            {organizing ? "Organizando..." : "Organizar com IA"}
          </button>
        )}
        <button
          type="button"
          onClick={onClear}
          disabled={lines.length === 0}
          className="ml-auto text-xs text-muted-foreground transition hover:text-foreground disabled:opacity-40"
        >
          Limpar
        </button>
      </div>

      <div ref={listRef} className="flex-1 space-y-3 overflow-y-auto px-5 py-2">
        {!supported ? (
          <p className="text-xs text-muted-foreground">
            Seu navegador não suporta transcrição por voz nativa. Tente o Chrome.
          </p>
        ) : lines.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center text-center">
            <p className="text-sm font-medium text-foreground">Sem transcrição ainda</p>
            <p className="mt-1 max-w-[240px] text-xs text-muted-foreground">
              Clique em "Transcrever" para converter fala em texto. Cada participante transcreve o
              próprio microfone e tudo é reunido aqui, num único documento.
            </p>
          </div>
        ) : (
          lines.map((l) => (
            <div key={l.id} className="text-sm leading-relaxed">
              <span className="mr-2 text-[11px] text-muted-foreground">{formatTime(l.ts)}</span>
              <span className={`font-medium ${l.self ? "text-primary" : "text-foreground"}`}>
                {l.speaker}:
              </span>{" "}
              <span className="text-foreground/90">{l.text}</span>
            </div>
          ))
        )}
      </div>
    </aside>
  );
}
