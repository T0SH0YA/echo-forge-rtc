
# WebRTC próprio — só a tecnologia, sem portal

Foco: código da stack. Hospedagem (VM, IP, portas) é commodity — terceirizamos pra AWS/Hetzner. O que importa é que **sinalização, STUN, TURN, SFU e SDK são código nosso**.

## Stack que vamos escrever

- **SDK cliente** (`sdk/`) — TypeScript, embrulha `RTCPeerConnection` do browser. API própria: `connect`, `publish`, `subscribe`, `room.on(...)`. Sem dependência de SaaS.
- **Sinalização** (`server/signaling/`) — Go + WebSocket, protocolo JSON nosso (`join/offer/answer/ice/leave`), emite credenciais TURN efêmeras.
- **STUN** (`server/stun/`) — Go, do zero, RFC 5389. ~600 LOC.
- **TURN** (`server/turn/`) — Go, do zero, RFC 5766/8656. Relay UDP/TCP/TLS, allocations, channels, permissions.
- **SFU** (`server/sfu/`) — Go. Stack ICE/DTLS/SRTP construído camada por camada. Forwarding RTP, simulcast, RTCP feedback (NACK/PLI/REMB/transport-cc).
- **Playground** (`playground/`) — página HTML mínima neste projeto Lovable só pra testar o SDK contra os servidores enquanto desenvolvemos. Não é landing, é bancada de teste.

Tudo vive **neste repositório**, em pastas separadas. Lovable é nosso editor + git + preview do playground. Os binários Go você roda numa VM (eu te dou Dockerfile + comando).

## Estrutura de pastas

```text
/sdk                    # TypeScript, publica no npm
/server/signaling       # Go
/server/stun            # Go
/server/turn            # Go
/server/sfu             # Go
/server/common          # tipos/utils Go compartilhados
/infra                  # Dockerfiles, docker-compose, deploy.sh
/playground             # página de teste (rota / do Lovable)
/docs/protocol          # specs internas (formato de mensagens, etc.)
```

## Etapas (sem prazos, cada uma testável)

### Etapa 1 — Esqueleto + protocolo
- Criar estrutura de pastas acima
- Definir e documentar o **protocolo de sinalização** (mensagens JSON, ordem, erros) em `docs/protocol/signaling.md`
- Definir API pública do SDK em `docs/protocol/sdk.md`
- Setup Go workspace (`go.work`) com módulos para cada server
- Setup SDK TS com build (tsup) pronto pra `npm publish`

### Etapa 2 — Sinalização + SDK + sala P2P ✅
- [x] Servidor `signaling` Go (gorilla/websocket): salas em memória, broadcast SDP/ICE, ping/pong, hello-timeout, room GC
- [x] SDK com perfect negotiation, mesh P2P, publishCamera/Screen, eventos
- [x] Transporte `bc://` (BroadcastChannel) pra testar duas abas sem servidor
- [x] Playground real (conectar, publicar, ver remotos)
- [ ] STUN/TURN: ainda nenhum (LAN/loopback funciona; pra cross-NAT precisa Etapa 3/4)

### Etapa 3 — STUN próprio ✅
- [x] RFC 5389 em Go puro: header, parser/encoder TLV, padding 4-byte
- [x] XOR-MAPPED-ADDRESS IPv4 e IPv6 (encode/decode)
- [x] MESSAGE-INTEGRITY HMAC-SHA1 com length-rewriting
- [x] FINGERPRINT CRC-32 (XOR 0x5354554E)
- [x] Servidor UDP responde Binding Request com XOR-MAPPED + SOFTWARE + FINGERPRINT
- [x] Testes unitários + smoke test end-to-end (`go test` verde)
- [ ] Integrar como `iceServers` no SDK (entra junto com Etapa 4 TURN)

### Etapa 4 — TURN próprio ✅
- [x] Codec STUN portado pro módulo turn + atributos TURN (CHANNEL-NUMBER, LIFETIME, XOR-PEER/RELAYED-ADDRESS, DATA, REQUESTED-TRANSPORT, ERROR-CODE)
- [x] msgType/methodOf/classOf para todas as classes (request/indication/success/error)
- [x] Long-term credentials RFC 5389: REALM + NONCE + MESSAGE-INTEGRITY HMAC-SHA1 com chave MD5(user:realm:pass)
- [x] Ephemeral credentials formato coturn: username "expiry:user", password base64(HMAC-SHA1(secret, username))
- [x] Allocate (request→401 challenge→success com XOR-RELAYED-ADDRESS + LIFETIME), Refresh, CreatePermission, ChannelBind
- [x] Relay UDP→UDP: socket dedicado por allocation, Send indication (cliente→peer) e Data indication (peer→cliente)
- [x] ChannelData (caminho rápido bidirecional, framing 0x4000-0x7FFE)
- [x] Binding request (compat STUN) na mesma porta
- [x] GC de allocations expiradas + estatísticas
- [x] Testes E2E: Allocate flow completo + round-trip Send/Data + ephemeral creds + Binding
- [ ] TCP/TLS (5349), DONT-FRAGMENT, RESERVATION-TOKEN: fora desta etapa
- [ ] Sinalização passar a emitir creds efêmeras e SDK consumir `iceServers`: entra junto da Etapa 5 (SFU)

### Etapa 5 — SFU: ICE no servidor ✅
- [x] Codec STUN local no módulo SFU + atributos ICE (USERNAME, PRIORITY, USE-CANDIDATE, ICE-CONTROLLED/CONTROLLING)
- [x] Parser SDP tolerante: extrai ice-ufrag/ice-pwd/fingerprint/setup/mid + BUNDLE
- [x] Gerador de answer ICE-lite com candidato host UDP único e atributos rtpmap/fmtp/rtcp-fb devolvidos
- [x] HTTP POST /sessions (offer JSON → answer JSON + sessionId), credenciais ICE aleatórias por sessão
- [x] UDP single-port listener: responde Binding Request com USERNAME ("localUfrag:remoteUfrag"), valida MI com LocalPwd, anexa MI+FINGERPRINT na resposta
- [x] USE-CANDIDATE marca sessão como ICEConnected fixando endereço remoto
- [x] Testes E2E: SDP parse+answer, fluxo HTTP+UDP até ICEConnected, rejeição de MI inválido
- [ ] Fingerprint do answer ainda é placeholder — DTLS real entra na Etapa 6

### Etapa 6 — SFU: DTLS handshake ✅
- [x] Cert ECDSA P-256 self-signed gerado por sessão (`generateDTLSCert`)
- [x] Fingerprint SHA-256 formatado "sha-256 AA:BB:..." e embarcado no `a=fingerprint` do answer
- [x] Verificação cruzada: `VerifyPeerCertificate` compara cert apresentado pelo peer com fingerprint do offer (defesa MITM dentro do túnel ICE)
- [x] Demux RFC 7983 no UDP single-port: STUN (magic cookie obrigatório) vs DTLS (b0 ∈ [20,63])
- [x] `dtlsPacketConn` — net.Conn adapter alimentado pelo udpLoop, escreve via UDPConn principal
- [x] Handshake dispara automaticamente quando ICE chega a Connected (USE-CANDIDATE)
- [x] Extração RFC 5705 com label "EXTRACTOR-dtls_srtp" → ClientKey/ServerKey/ClientSalt/ServerSalt prontos pra SRTP
- [x] Profiles negociados: SRTP_AEAD_AES_128_GCM (preferido) + SRTP_AES128_CM_HMAC_SHA1_80 (fallback)
- [x] Engine: pion/dtls v2 (MIT). Cert gen, fingerprint, verificação e EKM são código nosso.
- [x] Testes E2E: fingerprint format/match + handshake real (pion como cliente) → DTLSEstablished + keys extraídas

### Etapa 7 — SFU: SRTP + forwarding 1→N ✅
- [x] Parser RTP (`rtp.go`): header + CSRCs + header extension, calcula `HeaderLen` pra separar AAD do payload; demux RFC 7983 (`IsRTPOrRTCP`, `IsRTCP` via PT∈[64,95] da RFC 5761)
- [x] SRTP AES-128-GCM (`srtp.go`, RFC 7714): IV = salt XOR (00 00‖SSRC‖ROC‖SEQ), AAD = RTP header, Seal/Open com tag 16B; tracking ROC per-SSRC com detecção de wrap
- [x] Contextos por sessão: `srtpRecv` = ClientKey/ClientSalt (decifra publisher), `srtpSend` = ServerKey/ServerSalt (cifra pra subscriber); inicializados quando DTLS estabelece
- [x] Router 1→N (`router.go`): decifra com contexto do publisher, re-cifra com contexto de cada subscriber preservando SSRC/SEQ, envia pelo socket UDP único
- [x] Demux UDP atualizado: STUN | DTLS | RTP→router | RTCP (contado, forwarding em etapa futura)
- [x] Stats expostas: rtp_in / rtp_fwd / rtp_drop
- [x] Testes: roundtrip GCM, rejeição de pacote adulterado (auth tag), wrap de ROC, forwarding 1→2 com decifragem por chaves distintas em cada subscriber, demux RTP vs RTCP vs STUN
- [ ] SRTCP (E-bit + index distinto), AES-CM+HMAC-SHA1 fallback, simulcast/SVC: etapas seguintes

### Etapa 8 — SFU: SRTCP + feedback upstream ✅
- [x] SRTCP AES-128-GCM (`srtcp.go`, RFC 7714 §9): IV = salt XOR (00 00‖SSRC‖00 00‖idx31), AAD = pkt[0..7]‖trailer(E||idx), layout `[hdr8][cipher][tag16][E||idx4]`. Index TX monotônico per-contexto.
- [x] Parser RTCP compound (`rtcp.go`): split por length-words, helpers `IsPLI`/`IsNACK`/`IsTransportCC`, builder `BuildPLI` (PSFB fmt=1, 12B).
- [x] Router: tracking de SSRC→publisher (`trackSSRC` em todo RTP recebido). `HandleRTCP` decifra SRTCP do subscriber, parseia compound, agrupa feedback (PLI/NACK/transport-cc) por owner do mediaSSRC e reencaminha cifrado com `srtcpSend` do publisher.
- [x] `srtcpRecv`/`srtcpSend` por sessão (mesmas chaves SRTP, RFC 7714 §9); init disparado quando DTLS estabelece.
- [x] Stats: `rtcp_in`/`rtcp_fwd`/`rtcp_fb`.
- [x] Testes: roundtrip SRTCP, rejeição de tampering, split compound RR+PLI, E2E `RouterFeedbackUpstream` (publisher publica SSRC X, subscriber manda PLI cifrado, publisher recebe PLI cifrado com sua chave de servidor).
- [ ] RTX cache local (responder NACK sem ida ao publisher), transport-cc loop completo (BWE GCC) e jitter buffer: etapas seguintes.

### Etapa 9 — SFU: RTX cache + NACK responder local ✅
- [x] Ring buffer per-SSRC de 1024 pacotes RTP plaintext (`rtxcache.go`): `Put(ssrc,seq,headerLen,plain)` em todo RTP recebido, `Get(ssrc,seq)` por hash `seq%N` + validação exata.
- [x] NACK FCI codec (`rtcp.go`): `BuildNACK(senderSSRC, mediaSSRC, []seq)` agrupa em FCIs (PID+BLP de 16 bits) reusando uma PID por bloco contíguo até 17 seqs; `ParseNACK` expande FCIs em lista de seqs.
- [x] Router consome NACK localmente: olha no `RTXCache` do publisher dono do `mediaSSRC`, re-cifra cada pacote com `srtpSend` do solicitante e devolve preservando SSRC/SEQ. NACK NÃO sobe pro publisher.
- [x] PLI, transport-cc e demais FB continuam roteados upstream (Etapa 8).
- [x] Stats novas: `rtx_hit` / `rtx_miss`.
- [x] Testes: `BuildNACK`↔`ParseNACK` roundtrip com gap > 16; ring `Put`/`Get` (hit/miss/SSRC isolation); E2E `RouterRTXAnswersNACK` — 5 pacotes cached, subscriber pede {101,103}, recebe os dois RTX decifráveis com sua chave de servidor, publisher NÃO recebe nada.
- [ ] Simulcast (3 camadas Chrome, layer selection por subscriber via BWE), RTX com SSRC/PT dedicados (RFC 4588), transport-cc loop completo (GCC) e jitter buffer: etapa 10.

### Etapa 10 — SFU: simulcast + layer selection + REMB ✅
- [x] SDP parser estende: `a=rid:<id> send`, `a=simulcast:send q;h;f`, `a=extmap:<id> urn:…:rtp-stream-id` (+ repaired-rtp-stream-id) capturados per-media (`Media.RIDs`, `Simulcast`, `RIDExtID`, `RRIDExtID`).
- [x] BuildAnswer espelha simulcast e RIDs: `send q;h;f` → `recv q;h;f`, `a=rid:<id> recv` por camada.
- [x] Parser RFC 8285 one-byte header extension (`ParseOneByteExt`) extrai RID por pacote; sessão registra `rid↔ssrc` na primeira ocorrência (`rememberLayer`/`layerOfSSRC`/`availableLayers`).
- [x] Detecção de keyframe best-effort: VP8 (RFC 7741 §4.3) + H.264 (Single NAL IDR/SPS/PPS, STAP-A, FU-A start).
- [x] Layer selection per-subscriber (`prefLayer` no Session, fallback = camada mais alta disponível). Router filtra `shouldForward` por camada — só passa a camada preferida pra cada subscriber.
- [x] Endpoint `POST /sessions/{subID}/layer` body `{publisherId, rid}`: atualiza preferência E manda PLI cifrado pro publisher acelerar keyframe da nova camada.
- [x] REMB builder (`BuildREMB`): PSFB fmt=15 com "REMB" identifier, exp(6)+mantissa(18), pronto pra emitir bitrate target consolidado pro publisher.
- [x] Testes: parse one-byte ext (+ padding), LayerRank q/h/f e l/m/h, SDP simulcast parse+answer espelhamento, Session remember/avail/pref, REMB byte layout + decode roundtrip, VP8/H264 keyframe heuristics.
- [ ] BWE GCC completo (transport-cc feedback loop + arrival-time filter) e auto-switch baseado em loss/jitter: etapa 11.

### Etapa 11 — DataChannel (SCTP/DCEP) ✅
- [x] Associação SCTP sobre `*dtls.Conn` (pion/sctp, MIT) levantada automaticamente quando DTLS estabelece (`startSCTP` em `sctp.go`).
- [x] DCEP (RFC 8832): parser `ParseDCEPOpen` (label+protocol+priority+reliability), `BuildDCEPAck` e `buildDCEPOpen` próprios; respondemos ACK ao receber OPEN do browser.
- [x] PPIDs RFC 8831 (50/51/53/56/57) demuxados: DCEP tratado localmente, payloads de aplicação (string/binary) roteados.
- [x] Forwarding 1→N por label: mensagem chega no stream "chat" do publisher → SFU abre stream "chat" em cada subscriber (com DCEP_OPEN), encaminha preservando PPID.
- [x] Stats: `sctp` (associações), `dc` (channels), `dc_in` / `dc_fwd` (mensagens).
- [x] Testes: DCEP parse válido/inválido/truncado, roundtrip build↔parse, ACK byte exato, campo protocol não-vazio.
- [ ] Negociação SID via DCEP completa (par/ímpar conforme DTLS role), reliability não-confiável (max retransmits / max lifetime), API no SDK (`room.sendData`): seguem na etapa 12.

### Etapa 12 — DataChannel (continuação anterior)


- SCTP sobre DTLS (RFC 8831)
- Mensagens binárias e texto, ordered/unordered
- API no SDK: `room.sendData(peerId, payload)`

### Etapa 11 — Endurecimento
- Reconexão automática (ICE restart) no SDK
- Métricas Prometheus em todos os servers
- Load test (artillery + headless chrome) — 100, 500, 1000 peers por SFU
- TURN/SFU em múltiplas regiões + roteamento

### Etapa 12 — Extras vendáveis (quando o core estiver sólido)
- Gravação server-side (mux RTP→ffmpeg)
- RTMP egress
- E2EE via Insertable Streams (passthrough no SFU)
- SDKs Swift/Kotlin

## O que faço no próximo turno (Etapa 1)

1. Criar pastas: `/sdk`, `/server/{signaling,stun,turn,sfu,common}`, `/infra`, `/playground`, `/docs/protocol`
2. `go.work` + `go.mod` de cada módulo Go (stubs `main.go` que só logam "ok")
3. SDK TS com `package.json`, `tsconfig`, `tsup`, esqueleto de classes `Client`, `Room`, `LocalTrack`, `RemoteTrack`
4. `docs/protocol/signaling.md` — especificação completa do protocolo de mensagens
5. `docs/protocol/sdk.md` — API pública do SDK
6. `/playground` — página simples no Lovable com input de URL/token e dois `<video>` (placeholder, sem lógica ainda)
7. `infra/README.md` com instrução de `docker compose up` local

Sem cloud, sem auth, sem billing, sem landing. Só código.

## Confirmação pra eu começar

Só me responde **"vai"** e eu executo a Etapa 1. Não preciso de nome de produto, domínio nem cloud agora — isso só importa muito mais tarde.
