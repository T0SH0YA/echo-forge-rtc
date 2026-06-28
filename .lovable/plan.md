## Objetivo
Apontar o SDK do preview Lovable para o seu signaling em produção (`wss://sig.teli.app.br`) e validar que duas pessoas em redes/dispositivos diferentes entram na mesma sala.

## Passos

1. **Definir a variável no Lovable**
   - Adicionar `VITE_SIGNALING_URL=wss://sig.teli.app.br` no `.env` do projeto (Lovable lê variáveis `VITE_*` em build/preview).
   - Nenhuma mudança de código necessária: `src/routes/index.tsx` já faz `import.meta.env.VITE_SIGNALING_URL` com fallback pro `bc://`.

2. **Healthcheck rápido do servidor** (eu rodo `curl` quando estiver em build mode)
   - `curl -i https://sig.teli.app.br/healthz` → espera `200 ok`.
   - `curl -i -H "Upgrade: websocket" -H "Connection: Upgrade" -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" https://sig.teli.app.br/v1/rooms/ping?token=test` → espera `101 Switching Protocols`.
   - Se algum falhar, paro e te mostro o output antes de prosseguir (provável: DNS ainda não propagou, Caddy ainda emitindo cert, ou porta 443 fechada no firewall do Lightsail).

3. **Validar no preview**
   - Recarregar o preview do Lovable, abrir DevTools → Network → WS e confirmar conexão `wss://sig.teli.app.br/v1/rooms/<roomId>` com status `101`.
   - Confirmar que a mensagem `welcome` chega com `peerId` real do servidor Go (não mais `p_xxxx` do BroadcastChannel).

4. **Teste fim-a-fim entre dispositivos**
   - Abrir o preview no PC, entrar na sala, copiar link.
   - Abrir o mesmo link no celular (4G, fora do Wi-Fi) e confirmar vídeo+áudio bidirecional.
   - Se ICE não conectar entre redes NAT diferentes, é sinal de que falta TURN configurado no SDK — nesse caso, próxima etapa será expor TURN via `sig.teli.app.br` (ou subdomínio `turn.teli.app.br`) e injetar `iceServers` no `welcome` do signaling.

## O que NÃO faço nesta etapa
- Não mexo no servidor Go nem no Caddyfile (já estão no ar segundo você).
- Não adiciono UI nova; só ligo o cabo.

## Pergunta antes de implementar
Quer que eu já configure o STUN/TURN próprios (`stun:sig.teli.app.br:3478` / `turn:sig.teli.app.br:3478`) no `welcome` do signaling agora, ou primeiro só valida P2P puro com este signaling e tratamos TURN como próxima etapa se a conexão entre redes diferentes falhar?
