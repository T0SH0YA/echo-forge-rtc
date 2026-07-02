import { useMemo, useState } from "react";
import { X, Copy, Check, Download, Sparkles, Loader2 } from "lucide-react";
import type { OrganizedReport } from "../hooks/useAIOrganize";

interface AIReportModalProps {
  open: boolean;
  onClose: () => void;
  loading: boolean;
  error: string | null;
  report: OrganizedReport | null;
  meetingTitle?: string;
}

type Tab = "summary" | "topics" | "actions" | "minutes";

const TABS: { id: Tab; label: string }[] = [
  { id: "summary", label: "Resumo" },
  { id: "topics", label: "Tópicos" },
  { id: "actions", label: "Action items" },
  { id: "minutes", label: "Ata" },
];

function reportToMarkdown(r: OrganizedReport, title?: string): string {
  const lines: string[] = [];
  lines.push(`# ${title || "Reunião"} — Relatório\n`);
  lines.push(`## Resumo executivo\n${r.summary}\n`);
  lines.push(`## Tópicos e decisões`);
  for (const t of r.topics) {
    lines.push(`\n### ${t.title}`);
    for (const p of t.points) lines.push(`- ${p}`);
  }
  lines.push(`\n## Action items`);
  if (!r.actionItems.length) lines.push(`_Nenhum item registrado._`);
  for (const a of r.actionItems) {
    const meta = [a.owner && `**${a.owner}**`, a.due && `_até ${a.due}_`]
      .filter(Boolean)
      .join(" · ");
    lines.push(`- ${a.task}${meta ? ` — ${meta}` : ""}`);
  }
  lines.push(`\n## Ata\n${r.minutes}`);
  return lines.join("\n");
}

export function AIReportModal({
  open,
  onClose,
  loading,
  error,
  report,
  meetingTitle,
}: AIReportModalProps) {
  const [tab, setTab] = useState<Tab>("summary");
  const [copied, setCopied] = useState(false);

  const markdown = useMemo(
    () => (report ? reportToMarkdown(report, meetingTitle) : ""),
    [report, meetingTitle],
  );

  if (!open) return null;

  const copyAll = async () => {
    if (!markdown) return;
    try {
      await navigator.clipboard.writeText(markdown);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {}
  };

  const downloadMd = () => {
    if (!markdown) return;
    const blob = new Blob([markdown], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `teli-relatorio-${new Date().toISOString().slice(0, 10)}.md`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 p-4">
      <div className="flex h-[85vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-border/60 bg-card shadow-2xl">
        <header className="flex items-center justify-between border-b border-border/50 px-5 py-4">
          <div className="flex items-center gap-2">
            <Sparkles className="h-4 w-4 text-primary" />
            <h2 className="text-base font-semibold">Relatório da reunião</h2>
          </div>
          <button
            onClick={onClose}
            aria-label="Fechar"
            className="flex h-8 w-8 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted"
          >
            <X className="h-[18px] w-[18px]" />
          </button>
        </header>

        {loading ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
            <Loader2 className="h-8 w-8 animate-spin text-primary" />
            <p className="text-sm font-medium">Organizando com IA...</p>
            <p className="max-w-sm text-xs text-muted-foreground">
              Estamos processando a transcrição com GPT-5 mini. Pode levar alguns segundos.
            </p>
          </div>
        ) : error ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
            <p className="text-sm font-medium text-red-500">Não foi possível organizar</p>
            <p className="max-w-md text-xs text-muted-foreground">{error}</p>
          </div>
        ) : report ? (
          <>
            <nav className="flex border-b border-border/50 px-2">
              {TABS.map((t) => (
                <button
                  key={t.id}
                  onClick={() => setTab(t.id)}
                  className={`relative px-4 py-3 text-xs font-medium transition ${
                    tab === t.id
                      ? "text-foreground"
                      : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  {t.label}
                  {tab === t.id && (
                    <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-primary" />
                  )}
                </button>
              ))}
            </nav>

            <div className="flex-1 overflow-y-auto px-6 py-5 text-sm leading-relaxed">
              {tab === "summary" && (
                <p className="whitespace-pre-wrap text-foreground/90">{report.summary}</p>
              )}
              {tab === "topics" && (
                <div className="space-y-5">
                  {report.topics.length === 0 && (
                    <p className="text-xs text-muted-foreground">Nenhum tópico identificado.</p>
                  )}
                  {report.topics.map((t, i) => (
                    <div key={i}>
                      <h3 className="mb-2 text-sm font-semibold">{t.title}</h3>
                      <ul className="ml-5 list-disc space-y-1 text-foreground/90">
                        {t.points.map((p, j) => (
                          <li key={j}>{p}</li>
                        ))}
                      </ul>
                    </div>
                  ))}
                </div>
              )}
              {tab === "actions" && (
                <div className="space-y-2">
                  {report.actionItems.length === 0 ? (
                    <p className="text-xs text-muted-foreground">Nenhum action item registrado.</p>
                  ) : (
                    report.actionItems.map((a, i) => (
                      <div
                        key={i}
                        className="rounded-lg border border-border/60 bg-background/60 p-3"
                      >
                        <p className="text-sm text-foreground">{a.task}</p>
                        {(a.owner || a.due) && (
                          <p className="mt-1 text-[11px] text-muted-foreground">
                            {a.owner ? <span className="font-medium">{a.owner}</span> : null}
                            {a.owner && a.due ? " · " : ""}
                            {a.due ? <span>até {a.due}</span> : null}
                          </p>
                        )}
                      </div>
                    ))
                  )}
                </div>
              )}
              {tab === "minutes" && (
                <pre className="whitespace-pre-wrap font-sans text-foreground/90">
                  {report.minutes}
                </pre>
              )}
            </div>

            <footer className="flex items-center justify-end gap-2 border-t border-border/50 px-5 py-3">
              <button
                onClick={copyAll}
                className="inline-flex items-center gap-1.5 rounded-lg border border-border/60 px-3 py-2 text-xs font-medium text-muted-foreground transition hover:bg-muted hover:text-foreground"
              >
                {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                {copied ? "Copiado" : "Copiar tudo"}
              </button>
              <button
                onClick={downloadMd}
                className="inline-flex items-center gap-1.5 rounded-lg bg-primary px-3 py-2 text-xs font-semibold text-primary-foreground transition hover:opacity-90"
              >
                <Download className="h-3.5 w-3.5" />
                Baixar .md
              </button>
            </footer>
          </>
        ) : null}
      </div>
    </div>
  );
}
