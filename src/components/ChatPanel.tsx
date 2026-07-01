import { useEffect, useRef, useState } from "react";
import { Send, X } from "lucide-react";
import type { ChatMessage } from "../hooks/useChat";

interface ChatPanelProps {
  open: boolean;
  onClose: () => void;
  messages: ChatMessage[];
  onSend: (text: string) => void;
}

function formatTime(ts: number): string {
  try {
    return new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  } catch {
    return "";
  }
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

export function ChatPanel({ open, onClose, messages, onSend }: ChatPanelProps) {
  const [draft, setDraft] = useState("");
  const listRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight;
    }
  }, [messages, open]);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  const submit = () => {
    const text = draft.trim();
    if (!text) return;
    onSend(text);
    setDraft("");
  };

  if (!open) return null;

  return (
    <aside className="flex h-full w-full flex-col border-l border-white/10 bg-card/80 backdrop-blur-xl sm:w-80">
      <header className="flex items-center justify-between border-b border-white/5 px-4 py-3.5">
        <h2 className="text-sm font-semibold text-foreground">Mensagens</h2>
        <button
          type="button"
          onClick={onClose}
          aria-label="Fechar chat"
          className="flex h-8 w-8 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-white/10 hover:text-foreground"
        >
          <X className="h-4 w-4" />
        </button>
      </header>

      <div ref={listRef} className="flex-1 space-y-4 overflow-auto px-4 py-4">
        {messages.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center text-center">
            <p className="text-sm font-medium text-foreground">Nenhuma mensagem ainda</p>
            <p className="mt-1 text-xs text-muted-foreground">
              As mensagens ficam visíveis para todos nesta sala.
            </p>
          </div>
        ) : (
          messages.map((m) => (
            <div key={m.id} className={m.self ? "flex flex-col items-end" : "flex flex-col items-start"}>
              <div className="mb-1 flex items-center gap-2">
                {!m.self && (
                  <span className="flex h-6 w-6 items-center justify-center rounded-full bg-primary text-[10px] font-semibold text-primary-foreground">
                    {initials(m.author)}
                  </span>
                )}
                <span className="text-xs font-medium text-foreground">
                  {m.self ? "Você" : m.author}
                </span>
                <span className="text-[10px] text-muted-foreground">{formatTime(m.ts)}</span>
              </div>
              <div
                className={
                  m.self
                    ? "max-w-[85%] rounded-2xl rounded-tr-sm bg-primary px-3.5 py-2 text-sm text-primary-foreground shadow-md shadow-primary/20"
                    : "max-w-[85%] rounded-2xl rounded-tl-sm bg-white/10 px-3.5 py-2 text-sm text-foreground ring-1 ring-white/5"
                }
              >
                <span className="whitespace-pre-wrap break-words leading-relaxed">{m.text}</span>
              </div>
            </div>
          ))
        )}
      </div>

      <div className="border-t border-white/5 p-3">
        <div className="flex items-end gap-2 rounded-2xl border border-input bg-background/60 px-3.5 py-2.5 transition focus-within:border-primary/60 focus-within:ring-2 focus-within:ring-primary/40">
          <input
            ref={inputRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
            placeholder="Enviar mensagem para todos"
            className="min-w-0 flex-1 bg-transparent text-sm text-foreground outline-none placeholder:text-muted-foreground"
          />
          <button
            type="button"
            onClick={submit}
            disabled={!draft.trim()}
            aria-label="Enviar"
            className="flex h-8 w-8 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-md shadow-primary/25 transition hover:bg-primary/90 hover:scale-105 active:scale-95 disabled:opacity-40 disabled:shadow-none"
          >
            <Send className="h-4 w-4" />
          </button>
        </div>
      </div>
    </aside>
  );
}
