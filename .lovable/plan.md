## Atualizar `docs/embed.md`

Refletir o estado atual do projeto (URL publicada, secrets configurados) e adicionar troubleshooting pro caso "secrets trocados" que a gente acabou de resolver.

### Mudanças

1. **Confirmar URL publicada** em todos os exemplos: `https://echo-forge-rtc.lovable.app` (já está, só revisar consistência).

2. **Nova seção "Configuração dos secrets na Teli"** — deixar explícito o que vai em cada nome, pra não trocar de novo:

   | Secret (na Teli) | Valor |
   |---|---|
   | `TELI_RTC_TOKEN_URL` | `https://echo-forge-rtc.lovable.app/api/public/token` |
   | `TELI_RTC_API_KEY` | mesmo valor de `TELI_API_KEY` deste projeto |

3. **Nova seção "Troubleshooting"** com os erros mais comuns:
   - `TypeError: Invalid URL` no edge function da Teli → secrets trocados (URL foi salva no campo de API key).
   - `401 unauthorized` do `/api/public/token` → `TELI_RTC_API_KEY` da Teli ≠ `TELI_API_KEY` daqui.
   - `400 invalid roomId/userId` → regex `^[a-zA-Z0-9_-]{1,128}$` / `^[a-zA-Z0-9_.-]{1,128}$`.
   - Iframe abre mas câmera não pede permissão → faltou `allow="camera; microphone"`.
   - `postMessage` do parent ignorado → origem não bate com a allowlist de `src/lib/embed-bridge.ts`.

4. **Curl de smoke test** já pronto pra colar (com a URL real, não placeholder), pra validar rápido depois de configurar os secrets.

5. **Nota sobre secrets write-only**: lembrar que no Lovable os secrets não são legíveis depois de salvos — se perder o valor, gerar novo e atualizar dos dois lados.

Só documentação. Nenhum código de app/servidor é alterado.