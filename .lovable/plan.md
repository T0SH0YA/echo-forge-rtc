# Organização automática da transcrição com IA

Ao encerrar a chamada, o texto acumulado da transcrição é enviado para um endpoint server-side que chama `openai/gpt-5-mini` via Lovable AI Gateway e devolve 4 blocos: **Resumo executivo**, **Tópicos e decisões**, **Action items** e **Ata formatada**. O resultado aparece num modal com opções de copiar e baixar `.md`.

## Backend

**Novo:** `src/routes/api/organize-transcript.ts` (server route, streaming POST).
- Recebe `{ transcript: string, meetingTitle?: string, participants?: string[] }`.
- Usa helper Lovable AI Gateway (`src/lib/ai-gateway.server.ts`) — cria se não existir, seguindo o padrão da knowledge `ai-sdk-lovable-gateway`.
- `streamText` com `model = gateway("openai/gpt-5-mini")` e `providerOptions.lovable.service_tier: "priority"` (gpt-5-mini suporta fast mode).
- Structured output com `Output.object` (Zod schema com os 4 campos), provider criado com `{ structuredOutputs: true }`.
- System prompt em PT-BR pedindo os 4 blocos; trata 402 (créditos) e 429 (rate limit) devolvendo status apropriado.
- `LOVABLE_API_KEY` é auto-provisionado (checar/criar via `ai_gateway--create`).

## Frontend

**Novo:** `src/components/AIReportModal.tsx` — modal com abas (Resumo / Tópicos / Action items / Ata), estados loading/erro/pronto, botões **Copiar** e **Baixar .md**.

**Novo:** `src/hooks/useAIOrganize.ts` — dispara POST para `/api/organize-transcript`, gerencia loading/erro/resultado.

**Alterado:** `src/routes/index.tsx` — ao clicar em sair (PhoneOff), se `transcript.lines.length > 0` chama `organize(getFullText())` e mostra o modal; usuário pode fechar sem organizar.

**Alterado:** `src/components/TranscriptPanel.tsx` — corrigir o arquivo (hoje está duplicado — dois `import`/duas definições do mesmo componente causando parse error) e adicionar botão manual "Organizar com IA" no rodapé como atalho opcional.

## Notas técnicas

- Modelo: `openai/gpt-5-mini` (GPT-4o mini não existe no gateway; este é o equivalente moderno recomendado).
- Chamada 100% server-side; `LOVABLE_API_KEY` nunca vai pro browser.
- Transcrição continua sendo gerada no cliente (Web Speech API) como já é hoje — a IA só organiza o texto final.
- Custo é debitado dos créditos da workspace por request.
