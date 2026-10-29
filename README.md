<div align="center">

# 📞 WaCalls (Go)

**Native WhatsApp voice calls AND full messaging in pure Go, straight from the browser.**
Built for native VoIP media, multi-account (multi-session) operation, an Evolution
API–compatible REST surface, and a modern browser client.

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)](https://react.dev)
[![whatsmeow](https://img.shields.io/badge/whatsmeow-VoIP-25D366?logo=whatsapp&logoColor=white)](https://github.com/tulir/whatsmeow)
[![pion](https://img.shields.io/badge/pion-WebRTC-FF6B6B)](https://github.com/pion/webrtc)
[![Evolution API](https://img.shields.io/badge/compatible-Evolution%20API%20v2-blue)](#evolution-api-compatibility)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](#license)

[Overview](#overview) · [Architecture](#architecture) · [Quick Start](#quick-start) · [API](#api) · [Evolution](#evolution-api-compatibility) · [Security](#security)

</div>

---

## Overview

WaCalls pairs one or more WhatsApp accounts via **QR code** and gives you, from a single
Go process, both **1:1 native voice calls** from the browser and **full messaging**
(text, images, video, audio, documents, stickers) with a web chat UI, webhooks, and an
**Evolution API v2–compatible REST surface** — so existing Evolution clients and
automations work unchanged.

For calls, the browser microphone is sent as **raw 16 kHz PCM over a WebRTC data
channel** to the Go server, which encodes it with Meta's **MLow** codec and injects the
media into WhatsApp's **SRTP relay** mesh — and the reverse path brings the peer's audio
back to the browser.

The entire stack runs **natively in pure Go** (whatsmeow for the WhatsApp protocol,
pion for WebRTC, vendored pure-Go MLow codec): one binary, one session per account for
both messaging and calls. There is **no cgo and no native DLL**.

Multiple WhatsApp accounts can be paired and operated side by side, each with its own
pairing QR, connection status, chats, and call history. A single account can also run
**several concurrent 1:1 calls** at once — one per browser operator — routed
independently by call ID.

> **Status:** stable for calls; messaging (send/receive, media, groups, webhooks) is
> production-usable. Sessions, chats, messages and webhook configs persist in
> `wacalls.db` (pure-Go SQLite); media is cached under `data/media/`.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          BROWSER (React client)                            │
│   mic + speaker  ·  WebRTC data channel (16 kHz PCM)  ·  HTTP + SSE         │
└───────────────────────────────┬──────────────────────────────────────────┘
                                 │  POST /api/sessions/{sid}/calls/{id}/webrtc  (SDP)
                                 │  GET  /api/events                            (SSE)
                                 ▼
┌──────────────────────────── GO SERVER (cmd/server) ────────────────────────┐
│  SessionManager   registry of accounts (client + CallManager + bridge)     │
│  Broker           SSE hub (sessions, auth, call lifecycle fan-out)          │
│  Bridge           pion WebRTC bridge (16 kHz PCM data channel ⇄ call core)  │
│                                                                            │
│  internal/wa      VoipSocket adapter over whatsmeow                        │
│  internal/voip    call · signaling · media · transport · core · wanode     │
└───────────────┬──────────────────────────────────────┬────────────────────┘
                │ <call> signaling (Signal/USync)       │ SRTP media
                ▼                                        ▼
        ┌───────────────┐                    ┌──────────────────────┐
        │  WhatsApp WS  │                    │   WhatsApp relay      │
        │  (whatsmeow)  │                    │  (SRTP over SCTP/DC)  │
        └───────────────┘                    └──────────────────────┘
```

### Layout

| Path | Responsibility |
|---|---|
| `cmd/server` | HTTP/SSE broker, session manager + store, message store + webhooks, Evolution-compatible API, WebRTC bridge, process lifecycle |
| `internal/wa` | `VoipSocket` — sends/receives `<call>` stanzas via whatsmeow |
| `internal/voip/core` | Domain types, constants, the `VoipSocket` interface |
| `internal/voip/wanode` | Shared WhatsApp-node and JID helpers |
| `internal/voip/media` | MLow codec (vendored pure-Go `mlow/`), RTP, SRTP, SSRC, PCM helpers, key derivation |
| `internal/voip/transport` | SCTP relay, STUN, subscription encoding |
| `internal/voip/signaling` | `<call>` stanza build/parse, call-key crypto, relay-ack parsing |
| `internal/voip/call` | `CallManager` — orchestrates a single call end to end |
| `client/` | React 19 + Vite + Tailwind v4 + shadcn/ui (chat, dialer, call cards, sessions, history) |

---

## How a call flows

The core is `internal/voip/call.CallManager`, which drives a call end to end. Outgoing
call sequence:

```
1. POST .../calls            → CallManager.StartCall(peerJid)
                               generates a callID, builds the <call> offer, sends it

2. Browser opens WebRTC      → POST .../calls/{id}/webrtc (SDP offer)
                               the bridge answers with an SDP answer (pion)

3. Peer accepts              → events.CallAccept → HandleCallAccept
                               server receives <relay> + hop-by-hop keys

4. Relay transport           → STUN binding/allocate on WhatsApp relays
                               ICE + DTLS + SCTP DataChannel connect (pion)

5. SRTP media flowing        → state goes ACTIVE
   ├── uplink   (you → peer): browser 16 kHz PCM (data channel) → MLow encode → SRTP → relay
   └── downlink (peer → you): relay → SRTP → MLow decode → 16 kHz PCM (data channel) → browser

6. Teardown                  → DELETE .../calls/{id} or events.CallTerminate
                               CallManager.EndCall + bridge cleanup
```

Each protocol step (hop-by-hop SRTP key derivation, RTP packetization at `PT=120`/16 kHz,
STUN relay registration, relay-ack and `<call>` stanza parsing) is implemented and covered
by tests in `internal/voip` (`go test ./...`).

---

## Requirements

- **Go 1.26+**
- **Node 22+** and **npm** (only to build/run the React client)

No C compiler, cgo, or native libraries are required — the MLow codec is vendored
pure Go (`internal/voip/media/mlow`).

---

## Quick Start

```bash
# clone and enter the project
git clone <repo-url> wacalls-go
cd wacalls-go

# Go dependencies
go mod download

# React client dependencies
cd client && npm install && cd ..
```

### Run

```bash
go run ./cmd/server -addr :8080          # add -debug for verbose logs
```

Live audio works out of the box — the MLow codec is pure Go, so a plain build
includes it. No build tags, no `CGO_ENABLED`, no DLLs.

Open `http://localhost:8080`, click **New session**, and scan the QR shown in the browser
(it is also printed in the terminal) with **WhatsApp → Linked devices**. Add more accounts
the same way and switch between them in the sidebar.

### React client in dev mode

```bash
cd client
npm run dev      # Vite on :5173, proxies /api → http://localhost:8080
```

For production, build the static client and serve it from the Go server:

```bash
cd client && npm run build && cd ..
go run ./cmd/server -static client/dist -addr :8080
```

### Server flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8080` | HTTP listen address |
| `-db` | `wacalls.db` | SQLite session database path |
| `-static` | `client/dist` | Static client directory (optional) |
| `-debug` | `false` | Verbose logging (includes whatsmeow's internal log) |
| `-max-calls-per-session` | `8` | Max concurrent calls per session (`0` = unlimited) |
| `-api-key` | `""` | API key required on Evolution-compatible routes (header `apikey`); empty = no auth |
| `-data-dir` | `data` | Directory for cached media files |

---

## API

All routes are session-scoped. Events stream over a single SSE channel, tagged with the
originating `sessionId`.

| Method | Route | Purpose |
|---|---|---|
| `GET` | `/api/sessions` | List accounts (id, name, jid, status, paired) |
| `POST` | `/api/sessions` | Create an account and begin QR pairing |
| `DELETE` | `/api/sessions/{sid}` | Log out and remove an account |
| `POST` | `/api/sessions/{sid}/logout` | Disconnect an account (keep it for re-pairing) |
| `POST` | `/api/sessions/{sid}/pair` | Re-pair an account (emit a fresh QR) |
| `GET` | `/api/sessions/{sid}/chats` | Chat list (name, last message, unread) |
| `GET` | `/api/sessions/{sid}/messages?chat={jid}` | Messages of a chat (`limit`, `before` supported) |
| `POST` | `/api/sessions/{sid}/messages` | Send text (`{ chatJid \| number, text }`) |
| `POST` | `/api/sessions/{sid}/messages/media` | Send media (`{ chatJid, mediatype, media(base64), caption, fileName }`) |
| `POST` | `/api/sessions/{sid}/chats/read` | Mark a chat as read (`{ chatJid }`) |
| `POST` | `/api/sessions/{sid}/presence` | Send presence (`{ jid?, state: available\|composing\|recording\|paused }`) |
| `POST` | `/api/sessions/{sid}/check-number` | Check if numbers are on WhatsApp |
| `POST` | `/api/sessions/{sid}/profile-picture` | Fetch a profile picture URL |
| `GET` | `/api/media/{sid}/{file}` | Cached media files |
| `POST` | `/api/sessions/{sid}/calls` | Start an outgoing call (`{ phone, duration_ms?, record? }`) |
| `POST` | `/api/sessions/{sid}/calls/{id}/webrtc` | Exchange the browser WebRTC SDP |
| `POST` | `/api/sessions/{sid}/calls/{id}/accept` | Accept an incoming call |
| `POST` | `/api/sessions/{sid}/calls/{id}/reject` | Reject an incoming call |
| `DELETE` | `/api/sessions/{sid}/calls/{id}` | End an active call |
| `GET` | `/api/sessions/{sid}/history` | Recent call history (up to 50 records) |
| `GET` | `/api/events` | Server-sent events (sessions, auth, messages, calls) |

---

## Evolution API compatibility

The server exposes an **Evolution API v2–compatible REST surface** under the same route
layout (`/instance/...`, `/message/...`, `/chat/...`, `/group/...`, `/webhook/...`), so
tools and automations written for Evolution API work against WaCalls by just pointing
`baseUrl` here. Instances are WaCalls sessions: one QR pairing serves **both** messaging
and voice calls.

Authentication uses the `apikey` header (or `Authorization: Bearer`) matched against
`-api-key`.

```bash
# create an instance (starts QR pairing)
curl -X POST http://localhost:8080/instance/create \
  -H "Content-Type: application/json" -H "apikey: mykey" \
  -d '{"instanceName":"sales","webhook":{"enabled":true,"url":"https://my.app/hook"}}'

# fetch the QR
curl http://localhost:8080/instance/connect/sales -H "apikey: mykey"

# send a text message
curl -X POST http://localhost:8080/message/sendText/sales \
  -H "Content-Type: application/json" -H "apikey: mykey" \
  -d '{"number":"5511999999999","text":"Hello from WaCalls!"}'

# send an image (base64 or URL)
curl -X POST http://localhost:8080/message/sendMedia/sales \
  -H "Content-Type: application/json" -H "apikey: mykey" \
  -d '{"number":"5511999999999","mediatype":"image","media":"https://ex.com/pic.png","caption":"hi"}'
```

Supported route groups:

| Group | Routes |
|---|---|
| Instance | `POST /instance/create`, `GET /instance/fetchInstances`, `GET /instance/connect/{i}`, `GET /instance/connectionState/{i}`, `POST /instance/logout/{i}`, `POST /instance/reconnect/{i}`, `DELETE /instance/{i}` |
| Message | `POST /message/sendText/{i}`, `sendMedia`, `sendStatus`, `react`, `edit`, `delete` |
| Chat | `POST /chat/WhatsAppNumbers/{i}`, `checkIsRegisteredUser`, `fetchProfilePictureUrl`, `sendPresence`, `readMessages` |
| Group | `POST /group/create/{i}`, `GET /group/all/{i}`, `inviteCode`, `revokeInviteCode`, `join`, `leave`, `GET /group/participants/{i}/{g}`, `participants/add|remove|promote|demote`, `updateSubject`, `updateDescription`, `sendMessage` |
| Webhook | `POST /webhook/set/{i}`, `GET /webhook/find/{i}` |

Webhook events delivered as POSTs in the Evolution envelope
(`{ event, instance, destination, date_time, sender, data }`):
`MESSAGES_UPSERT`, `SEND_MESSAGE`, `MESSAGES_UPDATE`, `CONNECTION_UPDATE`,
`QRCODE_UPDATED`, `PRESENCE_UPDATE`.

---

## Tests

```bash
go test ./...                 # media stack: SRTP, STUN, RTP, relay-ack, codec, state
cd client && npm run build    # client type-check + production build
```

---

## Security

The `/api/*` routes have **no authentication** — anyone with HTTP access can create
accounts, place calls, send messages, and read history. **Run it only on a trusted LAN.**
Set `-api-key` to protect the Evolution-compatible routes. `wacalls.db` holds WhatsApp
session credentials (secrets) and `data/media/` caches message media: **do not commit
them** and keep them protected.

---

## Contributors

This project builds on the work of:

<div align="center">

<a href="https://github.com/jotadev66"><img src="https://github.com/jotadev66.png" width="72" height="72" style="border-radius:50%" alt="jotadev66"/></a>
<a href="https://github.com/jobasfernandes"><img src="https://github.com/jobasfernandes.png" width="72" height="72" style="border-radius:50%" alt="jobasfernandes"/></a>
<a href="https://github.com/edgardmessias"><img src="https://github.com/edgardmessias.png" width="72" height="72" style="border-radius:50%" alt="edgardmessias"/></a>
<a href="https://github.com/w3nder"><img src="https://github.com/w3nder.png" width="72" height="72" style="border-radius:50%" alt="w3nder"/></a>

[**@jotadev66**](https://github.com/jotadev66) · [**@jobasfernandes**](https://github.com/jobasfernandes) · [**@edgardmessias**](https://github.com/edgardmessias) · [**@w3nder**](https://github.com/w3nder)

</div>

---

## Acknowledgements

- [**whatsmeow**](https://github.com/tulir/whatsmeow) — Go WhatsApp Web protocol library
- [**pion/webrtc**](https://github.com/pion/webrtc) — pure-Go WebRTC stack (ICE + DTLS + SCTP)
- [**whatsapp-rust**](https://github.com/oxidezap/whatsapp-rust) — reference MLow codec implementation (ported to the vendored pure-Go `internal/voip/media/mlow`)
- [**zapo**](https://github.com/w3nder/zapo) — VoIP media-stack reference

---

## License

[MIT](./LICENSE)
