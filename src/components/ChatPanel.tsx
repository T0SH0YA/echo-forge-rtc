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
    <aside className="flex h-full w-full flex-col bg-card sm:w-[360px] sm:rounded-2xl sm:border sm:border-border/60">
      <header className="flex items-center justify-between px-5 pb-3 pt-4">
        <h2 className="text-base font-semibold text-foreground">Mensagens</h2>
        <button
          type="button"
          onClick={onClose}
          aria-label="Fechar chat"
          className="flex h-8 w-8 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted"
        >
          <X className="h-[18px] w-[18px]" />
        </button>
      </header>

      <div ref={listRef} className="flex-1 space-y-4 overflow-y-auto px-5 py-2">
        {messages.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center text-center">
            <p className="text-sm font-medium text-foreground">Nenhuma mensagem ainda</p>
            <p className="mt-1 max-w-[220px] text-xs text-muted-foreground">
              As mensagens enviadas na reuniao aparecem aqui.
            </p>
          </div>
        ) : (
          messages.map((m) => {
            const mine = m.self;
            return (
              <div key={m.id} className={`flex flex-col ${mine ? "items-end" : "items-start"}`}>
                {!mine && (
                  <span className="mb-1 px-1 text-xs font-medium text-muted-foreground">
                    {m.author}
                  </span>
                )}
                <div
                  className={`max-w-[85%] rounded-2xl px-3.5 py-2 text-sm leading-relaxed ${
                    mine
                      ? "rounded-br-md bg-primary text-primary-foreground"
                      : "rounded-bl-md bg-muted text-foreground"
                  }`}
                >
                  <p className="whitespace-pre-wrap break-words">{m.text}</p>
                </div>
                <span className="mt-1 px-1 text-[11px] text-muted-foreground">
                  {formatTime(m.ts)}
                </span>
              </div>
            );
          })
        )}
      </div>

      <div className="p-3">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
          className="flex items-center gap-2 rounded-full border border-border/60 bg-background py-1.5 pl-4 pr-1.5 transition-colors focus-within:border-primary"
        >
          <input
            ref={inputRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="Enviar mensagem"
            className="flex-1 bg-transparent text-sm text-foreground placeholder:text-muted-foreground focus:outline-none"
          />
          <button
            type="submit"
            disabled={!draft.trim()}
            aria-label="Enviar mensagem"
            className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-40"
          >
            <Send className="h-4 w-4" />
          </button>
        </form>
      </div>
    </aside>
  );
}
