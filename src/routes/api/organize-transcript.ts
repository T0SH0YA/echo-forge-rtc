import { createFileRoute } from "@tanstack/react-router";
import { generateText, Output } from "ai";
import { z } from "zod";
import { createLovableAiGatewayProvider } from "@/lib/ai-gateway.server";

const ReportSchema = z.object({
  summary: z.string().describe("Resumo executivo em 3-5 frases, em português."),
  topics: z
    .array(
      z.object({
        title: z.string(),
        points: z.array(z.string()),
      }),
    )
    .describe("Tópicos discutidos com bullets. Inclua decisões tomadas."),
  actionItems: z
    .array(
      z.object({
        task: z.string(),
        owner: z.string().nullable(),
        due: z.string().nullable(),
      }),
    )
    .describe("Tarefas acionáveis. owner e due podem ser null se não citados."),
  minutes: z
    .string()
    .describe(
      "Ata formatada em markdown: participantes, pauta, discussão, decisões, próximos passos.",
    ),
});

const InputSchema = z.object({
  transcript: z.string().min(1),
  meetingTitle: z.string().optional(),
  participants: z.array(z.string()).optional(),
});

export const Route = createFileRoute("/api/organize-transcript")({
  server: {
    handlers: {
      POST: async ({ request }) => {
        const key = process.env.LOVABLE_API_KEY;
        if (!key) return new Response("Missing LOVABLE_API_KEY", { status: 500 });

        let body: unknown;
        try {
          body = await request.json();
        } catch {
          return new Response("Invalid JSON", { status: 400 });
        }
        const parsed = InputSchema.safeParse(body);
        if (!parsed.success) {
          return new Response("Invalid input", { status: 400 });
        }
        const { transcript, meetingTitle, participants } = parsed.data;

        const gateway = createLovableAiGatewayProvider(key, { structuredOutputs: true });
        const model = gateway("openai/gpt-5-mini");

        const header = [
          meetingTitle ? `Título: ${meetingTitle}` : null,
          participants && participants.length ? `Participantes: ${participants.join(", ")}` : null,
        ]
          .filter(Boolean)
          .join("\n");

        try {
          const { experimental_output: output } = await generateText({
            model,
            experimental_output: Output.object({ schema: ReportSchema }),
            providerOptions: { lovable: { service_tier: "priority" } },
            system:
              "Você é um assistente que organiza transcrições de reuniões em português brasileiro. Seja fiel ao que foi dito, conciso e objetivo. Nunca invente informações. Se algo não estiver claro, omita.",
            prompt: `${header}\n\nTranscrição bruta (linha por fala):\n\n${transcript}`,
          });
          return Response.json(output);
        } catch (err) {
          const msg = (err as Error)?.message || "unknown";
          const status = /402/.test(msg)
            ? 402
            : /429/.test(msg)
              ? 429
              : 500;
          const friendly =
            status === 402
              ? "Créditos da workspace esgotados. Adicione créditos em Settings → Plans & credits."
              : status === 429
                ? "Muitas requisições. Tente novamente em instantes."
                : `Falha ao organizar transcrição: ${msg}`;
          return Response.json({ error: friendly }, { status });
        }
      },
    },
  },
});
