# Manual da API do CRM — WaCebrac

API REST para o CRM consumir: coletar leads e conversas, monitorar sessions e
ligações, e executar ações (ligar, mandar mensagem, gerenciar instâncias).

- **Base URL:** `http://localhost:11000` (ou o host onde o servidor roda)
- **Formato:** JSON, UTF-8
- **Rotas internas do painel** (`/api/sessions`, `/api/events`, ...) continuam
  funcionando sem token — esta API é um conjunto separado em `/api/crm/*`.

---

## 1. Autenticação

Toda requisição `/api/crm/*` exige dois headers:

| Header       | Obrigatório | Descrição |
|--------------|-------------|-----------|
| `apikey`     | sim         | Chave única do servidor, definida no flag `-api-key` (mesma das rotas Evolution; `Authorization: Bearer <chave>` também é aceito) |
| `X-User-Id`  | sim         | Identidade do colaborador — é ela que define de quem é cada lead e cada ligação |

```bash
curl -H "apikey: SUA_CHAVE" -H "X-User-Id: ana.paula" http://localhost:11000/api/crm/status
```

| Código | Quando |
|--------|--------|
| `503`  | Servidor iniciado sem `-api-key` (API desabilitada) |
| `403`  | `apikey` ausente ou incorreto |
| `400`  | `X-User-Id` ausente |

> **Importante:** sem `-api-key` no start do servidor, as rotas `/api/crm/*`
> respondem `503`. Ao definir a chave, as rotas Evolution também passam a
> exigir `apikey` (elas usam a mesma chave).

---

## 2. Regras de escopo (quem vê o quê)

- **Lead** = um contato que alguém clicou. Na primeira toque
  (`POST /leads`, `POST /calls`, `GET|POST /leads/{phone}/messages`) o lead é
  **registrado com dono fixo** = o `X-User-Id` daquele pedido.
- Um lead já pertencente a outro colaborador responde `403` — ninguém
  enxerga nem liga para lead dos outros.
- Cada lead fica preso à **instância (session) onde foi criado**.
- Antes de tudo, o colaborador precisa de uma **instância de origem** definida
  (`PUT /api/crm/origin`); sem ela os endpoints de lead respondem `409`.
- Os eventos em tempo real (`/api/crm/events`) são **filtrados por dono**:
  mensagens de leads seus, ligações suas e o estado das sessions.

---

## 3. Monitoração

### `GET /api/crm/status`

```json
{
  "origin": "890369f8...",
  "sessions": [ {"id": "...", "name": "Comercial 01", "jid": "...", "state": "open", "paired": true} ],
  "activeCalls": [ {"callId": "...", "sessionId": "...", "owner": "ana.paula", "peer": "5541...@s.whatsapp.net", "status": "ringing", "startedAt": 1793298797460} ]
}
```

### `GET /api/crm/events` (SSE)

Stream `text/event-stream` com keepalive `: ping` a cada 20 s. Eventos
entregues:

| Evento | Conteúdo |
|--------|----------|
| `session-list` | estado de todas as instances (para a tela de configurações) |
| `auth-state` / `session-qr` | pareamento/QR em andamento |
| `message` | mensagem de lead **cujo dono é o X-User-Id** |
| `call-status` / `call-ended` | ligações **com owner = X-User-Id** |
| `call-list` | lista de ligações ativas já filtrada pelas suas |

Outros eventos (mensagens de outros leads, presence, chamadas recebidas) são
descartados. Exemplo:

```bash
curl -N -H "apikey: CHAVE" -H "X-User-Id: ana.paula" http://localhost:11000/api/crm/events
```

---

## 4. Configurações do CRM — instâncias/sessions

| Método | Rota | Body | Sucesso |
|--------|------|------|---------|
| `GET`    | `/api/crm/sessions` | — | `200 {"sessions":[...]}` |
| `POST`   | `/api/crm/sessions` | `{"name":"Comercial 01"}` | `200 {"id":"<hex>"}` — inicia o pareamento |
| `PATCH`  | `/api/crm/sessions/{sid}` | `{"name":"Novo nome"}` | `204` — renomeia |
| `DELETE` | `/api/crm/sessions/{sid}` | — | `204` — desconecta e exclui |
| `GET`    | `/api/crm/sessions/{sid}/qr` | — | `200 {"state","paired","qr","qrBase64"}` |
| `POST`   | `/api/crm/sessions/{sid}/disconnect` | — | `204` — desconecta (mantém a sessão; depois é possível re-parear) |

**QR:** após `POST /api/crm/sessions`, consulte `GET /api/crm/sessions/{sid}/qr`
até obter `qr`/`qrBase64` (data URL pronta para exibir) e escaneie no WhatsApp.
Quando `paired: true`, o QR some da resposta.

### Instância de origem do colaborador

Define qual instance cada colaborador usa para falar com os leads:

| Método | Rota | Body | Sucesso |
|--------|------|------|---------|
| `GET`    | `/api/crm/origin` | — | `200 {"sessionId":"..."}` (`""` se não definida) |
| `PUT`    | `/api/crm/origin` | `{"sessionId":"<sid>"}` | `204` (404 se a session não existe) |
| `DELETE` | `/api/crm/origin` | — | `204` — limpa a origem |

> A origem é usada **quando o lead é criado**. Leads já criados continuam na
> instância de origem de quando nasceram.

---

## 5. Leads

### `POST /api/crm/leads` — registrar/assumir lead (abra no modal)

```bash
curl -X POST -H "apikey: CHAVE" -H "X-User-Id: ana.paula" -H "Content-Type: application/json" \
  -d '{"phone":"(41) 99123-5566","name":"Paulo Henrique Nogueira"}' \
  http://localhost:11000/api/crm/leads
```

- `phone`: número do lead (com ou sem `+55`; `(` `)` `-` são ignorados).
- Resposta `200 {"lead":{phone,name,owner,sessionId,createdAt}}`.
- Idempotente: se já for seu, responde o mesmo lead; se for de outro, `403`.

### `GET /api/crm/leads` — seus leads

`200 {"leads":[{phone,name,owner,sessionId,createdAt}, ...]}` (mais recentes
primeiro).

### `GET /api/crm/leads/{phone}/messages` — conversa (botão "Conversar")

```bash
curl -H "apikey: CHAVE" -H "X-User-Id: ana.paula" \
  "http://localhost:11000/api/crm/leads/5541991235566/messages?limit=50"
```

- Query: `limit` (padrão 50, máx 200), `before` (timestamp em ms da última
  mensagem que você já tem — pagina para trás).
- Resposta: `{"lead":{...},"messages":[...]}` — **mensagens em ordem
  crescente** (mais antiga em cima, mais nova embaixo), prontas para render
  de conversa.
- Assume o lead automaticamente se ainda não tiver dono (mesmas regras do
  `POST /leads`).

### `POST /api/crm/leads/{phone}/messages` — enviar mensagem

```bash
curl -X POST -H "apikey: CHAVE" -H "X-User-Id: ana.paula" -H "Content-Type: application/json" \
  -d '{"text":"Olá! Tudo bem?"}' \
  http://localhost:11000/api/crm/leads/5541991235566/messages
```

`200 {"id":"<msgId>","timestamp":1793298797460}` · `400` sem texto ·
`503` instance não pareada · `404` instance do lead foi excluída.

---

## 6. Ligações

### `POST /api/crm/calls` — botão "Ligar via WhatsApp"

```bash
curl -X POST -H "apikey: CHAVE" -H "X-User-Id: ana.paula" -H "Content-Type: application/json" \
  -d '{"phone":"(41) 99123-5566","name":"Paulo Henrique Nogueira"}' \
  http://localhost:11000/api/crm/calls
```

Body:

| Campo | Tipo | Padrão | Descrição |
|-------|------|--------|-----------|
| `phone` | string | obrigatório | número do lead |
| `name` | string | — | usado para registrar o lead se ainda não existir |
| `record` | bool | `true` | grava a ligação |
| `duration_ms` | int | 300000 | duração máxima |

Resposta `200`:

```json
{"call": {"callId": "CB599C0C...", "sessionId": "890369f8..."}}
```

A ligação sai da **instância de origem do colaborador** (o lead nasce nela se
for o primeiro contato). Acompanhe por `GET /api/crm/status`
(`activeCalls`), pelo SSE (`call-status`/`call-ended`) ou pelo histórico.

Erros: `403` lead de outro colaborador · `409` sem origem / já em ligação ·
`429` limite de ligações simultâneas · `503` instance não pareada.

### `GET /api/crm/calls?limit=50` — histórico das SUAS ligações

```json
{"calls": [{"callId":"...","peer":"5541...","startedAt":...,"endedAt":...,
            "durationMs":29465,"endReason":"user_ended","recordingUrl":"/api/recordings/..."}]}
```

A `recordingUrl` é um link relativo — monte `{baseUrl}{recordingUrl}` para
abrir a gravação.

### `POST /api/crm/calls/{callId}/end` — encerrar

`204` · `404` ligação inexistente · `403` ligação de outro colaborador.

---

## 7. Fluxos do seu CRM

### Ao abrir o modal de um lead

```bash
curl -X POST ... /api/crm/leads -d '{"phone":"(41) 99123-5566","name":"Paulo Henrique Nogueira"}'
```

Isso fixa o dono. A partir daí só esse `X-User-Id` vê a conversa e pode ligar.

### Botão "Ligar via WhatsApp"

```bash
curl -X POST ... /api/crm/calls -d '{"phone":"(41) 99123-5566","name":"Paulo Henrique Nogueira"}'
```

### Botão "Conversar com o lead"

1. `GET /api/crm/leads/{phone}/messages` → renderiza a conversa
2. `POST /api/crm/leads/{phone}/messages` `{"text":"..."}` → envia
3. (opcional) `GET /api/crm/events` para receber respostas em tempo real

### Tela de configurações do CRM

1. `GET /api/crm/sessions` → lista instances
2. `POST /api/crm/sessions` → cria → `GET .../qr` → exibe QR → polling até
   `paired: true`
3. `PATCH /api/crm/sessions/{sid}` → renomeia
4. `POST /api/crm/sessions/{sid}/disconnect` → desconecta
5. `DELETE /api/crm/sessions/{sid}` → exclui
6. Para cada colaborador: `PUT /api/crm/origin {"sessionId":"..."}` → define
   a instância de origem dele (`X-User-Id` de quem faz a chamada define o dono
   da atribuição)

---

## 8. Códigos de erro (resumo)

| Código | Significado |
|--------|-------------|
| 400 | body inválido / header `X-User-Id` ausente |
| 403 | chave errada ou recurso pertence a outro colaborador |
| 404 | session/ligação inexistente |
| 409 | sem instância de origem · operador já em ligação |
| 429 | limite de ligações simultâneas por sessão |
| 500 | erro interno (mensagem em `{"error":"..."}`) |
| 503 | API desabilitada (sem `-api-key`) ou instance não pareada |
