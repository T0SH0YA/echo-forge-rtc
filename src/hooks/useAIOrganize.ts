import { useCallback, useState } from "react";

export interface OrganizedReport {
  summary: string;
  topics: { title: string; points: string[] }[];
  actionItems: { task: string; owner: string | null; due: string | null }[];
  minutes: string;
}

interface OrganizeInput {
  transcript: string;
  meetingTitle?: string;
  participants?: string[];
}

export function useAIOrganize() {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<OrganizedReport | null>(null);
  const [open, setOpen] = useState(false);

  const organize = useCallback(async (input: OrganizeInput) => {
    setOpen(true);
    setLoading(true);
    setError(null);
    setReport(null);
    try {
      const res = await fetch("/api/organize-transcript", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        let msg = `HTTP ${res.status}`;
        try {
          const j = await res.json();
          if (j?.error) msg = j.error;
        } catch {}
        throw new Error(msg);
      }
      const data = (await res.json()) as OrganizedReport;
      setReport(data);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  const close = useCallback(() => setOpen(false), []);

  return { organize, close, open, loading, error, report };
}
