# Documentação da API WaCebrac — Guia de Integração

Esta é a referência geral para integrar qualquer coisa ao WaCebrac. Existem
**três superfícies** de API no mesmo servidor:

| Superfície | Rotas | Para quem | Autenticação |
|---|---|---|---|
| **[CRM](#1-crm-apicrm)** | `/api/crm/*` | Seu CRM (coletar, monitorar, executar) | `apikey` + `X-User-Id` |
| **[Painel](#2-painel-api)** | `/api/*` | A SPA operadora (Wa Cebrac) | nenhuma (identidade via `X-Client-Id`) |
| **[Evolution](#3-evolution-api)** | `/instance`, `/message`, `/chat`, `/group`, `/webhook` | Integrações compatíveis com Evolution API v2 | `apikey` |

**Especificação OpenAPI (máquina):** [`openapi.yaml`](./openapi.yaml) — 68
endpoints com schemas completos. Para visualizar:

```bash
npx @redocly/cli preview-docs openapi.yaml     # preview local
# ou cole o conteúdo em https://editor.swagger.io
```

**Guias por superfície:** este arquivo (visão geral + painel + Evolution) e
[`API-CRM.md`](./API-CRM.md) (CRM detalhado, fluxo por tela).

---

## Subindo o servidor

```powershell
$env:Path = "C:\Program Files\Go\bin;" + $env:Path
go build -o wa-cebrac.exe ./cmd/server
.\wa-cebrac.exe -api-key SUA_CHAVE [-debug] [-addr :11000] [-static client/dist]
```

| Flag | Efeito |
|---|---|
| `-api-key` | Habilita `/api/crm/*` (senão respondem **503**) e protege as rotas Evolution |
| `-debug` | Log verbose incluindo o tráfego cru do WhatsApp em `wa-cebrac.out.log` |
| `-max-calls-per-session` | Limite de ligações simultâneas por sessão (0 = ilimitado) → excedeu responde **429** |
| `-db` | SQLite (`wacalls.db`) ou `postgres://...` |
| `-static` | Serve o build do painel (`client/dist`) |

---

## Autenticação

```
apikey: SUA_CHAVE            # obrigatório em /api/crm/* e Evolution
X-User-Id: ana.paula         # obrigatório só no CRM (identidade do colaborador)
X-Client-Id: uuid-aleatorio  # opcional no painel (dono do operador/ligação)
```

A Evolution também aceita `Authorization: Bearer SUA_CHAVE`.

| Erro | Superfície | Quando |
|---|---|---|
| `403 {"error":"invalid api key"}` / `{"success":false,"error":{"code":"FORBIDDEN",...}}` | CRM / Evolution | chave ausente ou errada |
| `400 {"error":"X-User-Id header required"}` | CRM | colaborador não informado |
| `503 {"error":"CRM API disabled..."}` | CRM | servidor sem `-api-key` |
| `503 {"error":"not paired"}` | Painel/CRM | sessão ainda não pareada no WhatsApp |

---

## 1. CRM (`/api/crm/*`)

O manual completo — endpoints, regras de dono/origem, códigos de erro e os
fluxos dos botões "Ligar via WhatsApp" e "Conversar com o lead" — está em
**[`API-CRM.md`](./API-CRM.md)**. Resumo:

```bash
H=(-H "apikey: SUA_CHAVE" -H "X-User-Id: ana.paula")

# Monitorar
curl "${H[@]}" http://localhost:11000/api/crm/status
curl -N "${H[@]}" http://localhost:11000/api/crm/events      # SSE filtrado

# Configurar (settings do CRM)
curl "${H[@]}" http://localhost:11000/api/crm/sessions                       # listar
curl "${H[@]}" -X POST -H "Content-Type: application/json" \
  -d '{"name":"Comercial 01"}' http://localhost:11000/api/crm/sessions       # criar + QR
curl "${H[@]}" http://localhost:11000/api/crm/sessions/ID/qr                 # qrBase64
curl "${H[@]}" -X PATCH -H "Content-Type: application/json" -d '{"name":"Novo"}' \
  http://localhost:11000/api/crm/sessions/ID                                # renomear
curl "${H[@]}" -X POST http://localhost:11000/api/crm/sessions/ID/disconnect # desconectar
curl "${H[@]}" -X DELETE http://localhost:11000/api/crm/sessions/ID          # excluir
curl "${H[@]}" -X PUT -H "Content-Type: application/json" \
  -d '{"sessionId":"ID"}' http://localhost:11000/api/crm/origin              # origem do colaborador

# Leads + conversa + ligação
curl "${H[@]}" -X POST -H "Content-Type: application/json" \
  -d '{"phone":"(41) 99123-5566","name":"Paulo"}' http://localhost:11000/api/crm/leads
curl "${H[@]}" http://localhost:11000/api/crm/leads/5541991235566/messages
curl "${H[@]}" -X POST -H "Content-Type: application/json" -d '{"text":"Olá!"}' \
  http://localhost:11000/api/crm/leads/5541991235566/messages
curl "${H[@]}" -X POST -H "Content-Type: application/json" \
  -d '{"phone":"(41) 99123-5566","name":"Paulo"}' http://localhost:11000/api/crm/calls
curl "${H[@]}" -X POST http://localhost:11000/api/crm/calls/CALLID/end
```

**Garantias de escopo:** o primeiro a tocar no lead vira dono (`403` para os
demais); sem instância de origem definida os endpoints de lead respondem `409`;
o SSE entrega só mensagens/ligações do próprio colaborador.

---

## 2. Painel (`/api/*`)

Rotas usadas pela SPA — sem chave. Todas as rotas de sessão usam `{sid}` = ID da
instância (hex de 32 chars).

### Sessions

| Método/rota | Corpo | Sucesso |
|---|---|---|
| `GET /api/sessions` | — | `200 {"sessions":[{id,name,jid,state,paired}]}` |
| `POST /api/sessions` | `{"name"}` | `200 {"id"}` (inicia pareamento) |
| `DELETE /api/sessions/{sid}` | — | `204` |
| `GET /api/sessions/{sid}/qr` | — | `200 {state,paired,qr?,qrBase64?}` |
| `POST /api/sessions/{sid}/pair` | — | `204` / `400` |
| `POST /api/sessions/{sid}/logout` | — | `204` |

### Chats e mensagens

| Método/rota | Corpo/params | Sucesso |
|---|---|---|
| `GET /api/sessions/{sid}/chats` | — | `200 {"chats":[ChatSummary]}` (mais ativa primeiro) |
| `GET /api/sessions/{sid}/messages` | query `chat` (obrig.), `limit` (≤200), `before` (ms) | `200 {"messages":[StoredMessage]}` — **mais nova primeiro** |
| `POST /api/sessions/{sid}/messages` | `{number\|chatJid, text}` | `200 {"id","timestamp"}` |
| `POST /api/sessions/{sid}/messages/media` | `{number\|chatJid, mediatype, media, caption?, fileName?, ptt?, seconds?}` | `200 {"id","timestamp"}` |
| `POST /api/sessions/{sid}/messages/contact` | `{number\|chatJid, name, phone}` | `200 {"id","timestamp"}` |
| `POST /api/sessions/{sid}/chats/read` | `{chatJid}` | `200 {"status":"ok"}` |
| `POST /api/sessions/{sid}/presence` | `{jid?, state?}` (`recording`/`paused`/digitando) | `200 {"status":"ok"}` |
| `POST /api/sessions/{sid}/check-number` | `{numbers:[...]}` | `200 {"numbers":[{exists,jid,number}]}` |
| `POST /api/sessions/{sid}/profile-picture` | `{jid}` | `200 {"url"}` |
| `GET /api/media/{sid}/{file}` | — | binário (mídia recebida) |

- `media`: base64 puro, data URI (`data:<mime>;base64,...`) ou `http(s)://`,
  máx. 64 MB. `mediatype`: `image|video|audio|document|sticker`.
- **Nota:** o painel ordena as mensagens no cliente (mais novas embaixo);
  a API entrega DESC — integrações ordenam como preferirem.

### Ligações

| Método/rota | Corpo/params | Sucesso |
|---|---|---|
| `POST /api/sessions/{sid}/calls` | `{phone, duration_ms?, record?}` | `200 {"call":{"callId"}}` · `409` operador ocupado · `429` limite |
| `GET /api/sessions/{sid}/calls` | query `limit`, `peer` | `200 {"calls":[CallLog]}` (histórico persistido) |
| `GET /api/sessions/{sid}/calls/{id}` | — | `200 CallLog` (sem envelope) |
| `POST /api/sessions/{sid}/calls/{id}/webrtc` | `{sdp_offer}` | `200 {"sdp_answer"}` |
| `POST /api/sessions/{sid}/calls/{id}/accept` | — (usa `X-Client-Id`) | `200 {"call":{...}}` · `409` |
| `POST /api/sessions/{sid}/calls/{id}/reject` | — | `200 {"status":"ok"}` |
| `DELETE /api/sessions/{sid}/calls/{id}` | — | `204` |
| `GET /api/sessions/{sid}/history` | — | `200 {"rows":[LiveCall]}` (últimas 50 em memória) |
| `GET /api/recordings/{sid}/{file}` | — | `200` WAV (`CallLog.recordingUrl`) |

`CallLog` traz `durationMs`, `recordingMs`, `endReason`
(`user_ended|timeout|enc|busy|failed|declined`) e `recordingUrl` quando houve
gravação.

### Eventos (SSE)

```
GET /api/events            # header opcional X-Client-Id (ou ?clientId=)
Accept: text/event-stream
```

Mensagens no formato `data: {json}\n\n`, keepalive `: ping` a cada 20 s, com
snapshot inicial. Tipos de evento:

| `type` | Quando |
|---|---|
| `session-list`, `auth-state`, `session-qr` | mudança de estado/QR de instância |
| `message` | mensagem nova (`message` = StoredMessage) |
| `message-status`, `message-media` | entrega/leitura ou mídia baixada |
| `presence`, `chat-presence` | digitação/gravação do contato |
| `call-status`, `call-ended`, `call-list` | lifecycle das ligações |
| `incoming`, `incoming-claimed` | ligação recebida e quem assumiu |

```js
const es = new EventSource("http://localhost:11000/api/events?clientId=meu-uuid");
es.onmessage = (e) => { const ev = JSON.parse(e.data); /* ... */ };
```

---

## 3. Evolution API

Compatível com Evolution API v2 (formato de erro e respostas idênticos).
`{instance}` = nome/ID da instância. **Todas exigem `apikey`.**

**Formato de erro:** `{"success":false,"error":{"code":"BAD_REQUEST","message":"..."}}`
— codes: `BAD_REQUEST(400) | FORBIDDEN(403) | NOT_FOUND(404) | CONFLICT(409) |
INTERNAL(500) | SERVICE_UNAVAILABLE(503)`.

**Formato de mensagem enviada (201):**
`{"key":{"remoteJid","fromMe":true,"id"},"messageTimestamp":"<segundos>","status":"PENDING"}`.

### Instâncias

| Rota | Sucesso |
|---|---|
| `POST /instance/create` `{"instanceName", webhook?}` | `201 {"instance":{...,"status":"created"},"hash":{apikey,instanceName}}` |
| `GET /instance/fetchInstances` | `200 [{id,instanceName,state,number,profileName,...}]` |
| `GET /instance/connect/{instance}` | `200 {pairing,code,base64,count}` — polling até `pairing:false` (`503` = QR ainda não pronto) |
| `GET /instance/connectionState/{instance}` | `200 {"instance":{instanceName,state:"open"\|"close"\|"connecting"}}` |
| `POST /instance/logout/{instance}` | `200 {"instance":{status:"logout"}}` |
| `POST /instance/reconnect/{instance}` | `200 {"instance":{status:"connecting"}}` |
| `DELETE /instance/{instance}` | `200 {"instance":{status:"deleted"}}` |

### Mensagens (todas respondem `201`)

| Rota | Corpo principal |
|---|---|
| `POST /message/sendText/{instance}` | `{number, text, delay?}` (`textMessage.text` alternativo) |
| `POST /message/sendMedia/{instance}` | `{number, mediatype, media, caption?, ptt?, seconds?, fileName?}` |
| `POST /message/react/{instance}` | `{number, reaction, key{id,remoteJid,...}}` |
| `POST /message/delete/{instance}` | `{key{id,...}, onlyForMe?}` (`400` se `onlyForMe` em mensagem recebida) |
| `POST /message/edit/{instance}` | `{number, messageId, text}` |
| `POST /message/sendStatus/{instance}` | `{text \| media, mediatype?}` → `remoteJid = status@broadcast` |

> **Notas de implementação:**
> - `ptt: true` com áudio vira nota de voz real: o servidor converte para
>   OGG/Opus (requer `ffmpeg` no PATH — sem ele, cai para áudio comum).
> - `delay` dorme N ms antes de enviar.
> - `linkPreview`, `mentionsEveryOne`, `mentioned`, `contentType` são aceitos e
>   ignorados.

### Chats

| Rota | Sucesso |
|---|---|
| `POST /chat/WhatsAppNumbers/{instance}` `{"numbers":[...]}` | `200 [{exists,jid,number,name?}]` |
| `POST /chat/checkIsRegisteredUser/{instance}` `{"number"}` | `200 {exists,number}` |
| `POST /chat/fetchProfilePictureUrl/{instance}` `{number\|jid}` | `200 {url}` ("" se não houver) |
| `POST /chat/sendPresence/{instance}` `{number?,presence?}` | `200 {presence:"sent"}` |
| `POST /chat/readMessages/{instance}` `{"readMessages":[{id,remoteJid,...}]}` | `200 {read}` |

### Grupos

| Rota | Sucesso |
|---|---|
| `POST /group/create/{instance}` `{subject,participants?}` | `201 {creator,id,subject}` |
| `GET /group/all/{instance}` | `200 [{id,subject,size,participants[]}]` |
| `POST /group/inviteCode/{instance}` `{number}` (JID do grupo) | `200 {inviteCode,inviteUrl}` |
| `POST /group/revokeInviteCode/{instance}` `{number}` | `200 {inviteCode,inviteUrl}` |
| `POST /group/join/{instance}` `{inviteCode}` (código ou URL) | `200 {status:"success",group}` |
| `POST /group/leave/{instance}` `{number}` | `200 {status:"success"}` |
| `GET /group/participants/{instance}/{group}` | `200 GroupInfo` |
| `POST /group/participants/{add\|remove\|promote\|demote}/{instance}` `{groupJid,participants[]}` | `200 {participants[]}` |
| `POST /group/updateSubject/{instance}` `{number,subject}` | `200 {status:"success"}` |
| `POST /group/updateDescription/{instance}` `{number,description}` | `200 {status:"success"}` |
| `POST /group/sendMessage/{instance}` `{number,text}` | `201` mensagem |

### Webhooks

| Rota | Corpo/resposta |
|---|---|
| `POST /webhook/set/{instance}` | `{webhook:{enabled,url,webhookByEvents,events[]}}` → `200 {"webhooks":{...}}` |
| `GET /webhook/find/{instance}` | `200 {"webhooks":{...}}` (objeto vazio se não configurado) |

---

## Exemplos rápidos

### Criar instância e parear (fluxo completo)

```bash
BASE=http://localhost:11000
K="SUA_CHAVE"

# 1. cria
ID=$(curl -s -X POST -H "apikey: $K" -H "Content-Type: application/json" \
  -d '{"instanceName":"Comercial 01"}' $BASE/instance/create | python -c "import sys,json;print(json.load(sys.stdin)['instance']['instanceName'])")

# 2. polling do QR (repetir até pairing=false)
curl -s -H "apikey: $K" $BASE/instance/connect/$ID
# {"pairing":true,"code":"2@...","base64":"data:image/png;base64,...","count":1}
```

### Enviar áudio como nota de voz

```bash
curl -X POST -H "apikey: $K" -H "Content-Type: application/json" \
  -d '{"number":"5541991235566","mediatype":"audio","media":"'"$(base64 -w0 voz.mp3)"'","ptt":true,"seconds":7}' \
  $BASE/message/sendMedia/$ID
```

### Receber mensagens em tempo real (painel)

```js
const es = new EventSource("http://localhost:11000/api/events");
es.onmessage = (e) => {
  const ev = JSON.parse(e.data);
  if (ev.type === "message") console.log(ev.message.chatJid, ev.message.text);
};
```

---

## Resposta a erros (resumo)

| Status | Corpo (painel/CRM) | Corpo (Evolution) | Causa típica |
|---|---|---|---|
| 400 | `{"error"}` | `{"success":false,"error":{code,message}}` | body/header ausente |
| 403 | `{"error"}` | idem | chave errada / lead ou call de outro usuário |
| 404 | `{"error"}` | idem | sessão, ligação ou instância inexistente |
| 409 | `{"error"}` | idem | operador em ligação / sem instância de origem |
| 429 | `{"error"}` | — | ligações simultâneas acima do limite |
| 500 | `{"error"}` | idem | erro interno (mensagem do Go) |
| 503 | `{"error"}` | idem | não pareada · API CRM desabilitada |

---

## Observações operacionais

- **Gravações** ficam em `data/recordings/<sid>/*.wav` e são servidas por
  `/api/recordings/{sid}/{file}`; mídias recebidas em `data/media/...` via
  `/api/media/{sid}/{file}`. Ambos validam o nome do arquivo (sem travessia).
- **Ligação pelo navegador** usa dois passos: `POST .../calls` (sinalização
  WhatsApp) e `POST .../calls/{id}/webrtc` (troca SDP com a ponte). O CRM usa a
  API de CRM (1 passo) — o servidor monta tudo.
- **CORS** liberado para todos os métodos (`GET, POST, PUT, PATCH, DELETE`) com
  os headers `Content-Type, X-Client-Id, X-User-Id, apikey, Authorization`.
- **Eventos** são em memória (broker): reconectar com snapshot ao abrir o SSE.
- Chave da API em uso está em `.crm-api-key` (gitignored). Nunca commitar.
