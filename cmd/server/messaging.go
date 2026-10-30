package main

import (
	"context"
	"encoding/json"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type extractedMessage struct {
	Type     string
	Text     string
	FileName string
	MimeType string
}

func extractMessageContent(msg *waE2E.Message) extractedMessage {
	if msg == nil {
		return extractedMessage{Type: "unknown"}
	}
	switch {
	case msg.GetConversation() != "":
		return extractedMessage{Type: "text", Text: msg.GetConversation()}
	case msg.GetExtendedTextMessage() != nil:
		m := msg.GetExtendedTextMessage()
		return extractedMessage{Type: "text", Text: m.GetText()}
	case msg.GetImageMessage() != nil:
		m := msg.GetImageMessage()
		return extractedMessage{Type: "image", Text: m.GetCaption(), MimeType: m.GetMimetype()}
	case msg.GetVideoMessage() != nil:
		m := msg.GetVideoMessage()
		return extractedMessage{Type: "video", Text: m.GetCaption(), MimeType: m.GetMimetype()}
	case msg.GetAudioMessage() != nil:
		m := msg.GetAudioMessage()
		typ := "audio"
		if m.GetPTT() {
			typ = "ptt"
		}
		return extractedMessage{Type: typ, MimeType: m.GetMimetype()}
	case msg.GetDocumentMessage() != nil:
		m := msg.GetDocumentMessage()
		return extractedMessage{Type: "document", Text: m.GetCaption(), FileName: m.GetFileName(), MimeType: m.GetMimetype()}
	case msg.GetStickerMessage() != nil:
		m := msg.GetStickerMessage()
		return extractedMessage{Type: "sticker", MimeType: m.GetMimetype()}
	case msg.GetLocationMessage() != nil:
		return extractedMessage{Type: "location"}
	case msg.GetContactMessage() != nil:
		m := msg.GetContactMessage()
		return contactContent(m.GetDisplayName(), m.GetVcard())
	case msg.GetContactsArrayMessage() != nil:
		m := msg.GetContactsArrayMessage()
		if len(m.GetContacts()) == 0 {
			return extractedMessage{Type: "contact"}
		}
		first := m.GetContacts()[0]
		return contactContent(first.GetDisplayName(), first.GetVcard())
	case msg.GetReactionMessage() != nil:
		return extractedMessage{Type: "reaction", Text: msg.GetReactionMessage().GetText()}
	case msg.GetProtocolMessage() != nil:
		return extractedMessage{Type: "protocol"}
	case msg.GetPollCreationMessage() != nil:
		return extractedMessage{Type: "poll", Text: msg.GetPollCreationMessage().GetName()}
	case msg.GetPollCreationMessageV2() != nil:
		return extractedMessage{Type: "poll", Text: msg.GetPollCreationMessageV2().GetName()}
	case msg.GetPollCreationMessageV3() != nil:
		return extractedMessage{Type: "poll", Text: msg.GetPollCreationMessageV3().GetName()}
	case msg.GetListMessage() != nil:
		return extractedMessage{Type: "list"}
	case msg.GetButtonsMessage() != nil:
		return extractedMessage{Type: "buttons"}
	case msg.GetTemplateMessage() != nil:
		return extractedMessage{Type: "template"}
	default:
		return extractedMessage{Type: "unknown"}
	}
}

func downloadableOf(msg *waE2E.Message) (whatsmeow.DownloadableMessage, string) {
	switch {
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage(), "image"
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage(), "video"
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage(), "audio"
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage(), "document"
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage(), "sticker"
	default:
		return nil, ""
	}
}

var protoMarshal = protojson.MarshalOptions{EmitUnpopulated: false, UseProtoNames: false}

func (s *Session) handleMessageEvent(evt *events.Message) {
	if evt.Info.ID == "" || evt.Message == nil {
		return
	}
	ctx := context.Background()
	content := extractMessageContent(evt.Message)
	chatJID := s.canonicalChatJID(ctx, evt.Info.MessageSource)
	stored := &StoredMessage{
		ID:        evt.Info.ID,
		SessionID: s.id,
		ChatJID:   chatJID.String(),
		SenderJID: s.canonicalPeer(ctx, evt.Info.Sender).String(),
		FromMe:    evt.Info.IsFromMe,
		Type:      content.Type,
		Text:      content.Text,
		FileName:  content.FileName,
		MimeType:  content.MimeType,
		Status:    "RECEIVED",
		Timestamp: evt.Info.Timestamp.UnixMilli(),
		PushName:  evt.Info.PushName,
	}
	if stored.Timestamp == 0 {
		stored.Timestamp = time.Now().UnixMilli()
	}
	if evt.Info.IsFromMe {
		stored.Status = "DELIVERED"
	}
	if err := s.mgr.msgs.insertMessage(ctx, stored); err != nil {
		s.log.Warn("persist message failed", "err", err)
	}
	if evt.Info.PushName != "" && !evt.Info.IsGroup {
		_ = s.mgr.msgs.setChatName(ctx, s.id, stored.ChatJID, evt.Info.PushName)
	}

	var msgJSON json.RawMessage
	if raw, err := protoMarshal.Marshal(evt.Message); err == nil {
		msgJSON = raw
	}
	evData := map[string]any{
		"key": map[string]any{
			"remoteJid":   stored.ChatJID,
			"fromMe":      stored.FromMe,
			"id":          stored.ID,
			"participant": stored.SenderJID,
		},
		"pushName":         stored.PushName,
		"message":          msgJSON,
		"messageType":      stored.Type,
		"messageTimestamp": stored.Timestamp / 1000,
		"status":           stored.Status,
		"content": map[string]any{
			"type": stored.Type, "text": stored.Text,
			"fileName": stored.FileName, "mimeType": stored.MimeType,
			"mediaUrl": stored.MediaURL,
		},
	}
	webhookEvent := EvMessagesUpsert
	if stored.FromMe {
		webhookEvent = EvSendMessage
	}
	s.mgr.hooks.dispatch(ctx, s.id, s.id, webhookEvent, evData)
	s.mgr.broker.emitMessage(stored)

	if dl, kind := downloadableOf(evt.Message); dl != nil {
		go s.downloadIncomingMedia(evt.Info.ID, stored.ChatJID, dl, kind, content.MimeType, msgJSON, evData)
	}
}

func (s *Session) downloadIncomingMedia(msgID, chatJID string, dl whatsmeow.DownloadableMessage, kind, mime string, msgJSON json.RawMessage, evData map[string]any) {
	ctx := context.Background()
	data, err := s.client.Download(ctx, dl)
	if err != nil {
		s.log.Warn("media download failed", "msg", msgID, "err", err)
		return
	}
	ext := extForMime(mime)
	if name, err := s.mgr.media.save(s.id, msgID, ext, data); err == nil {
		if err := s.mgr.msgs.setMessageMedia(ctx, s.id, msgID, name); err != nil {
			s.log.Warn("update media path failed", "err", err)
		}
		mediaURL := "/api/media/" + s.id + "/" + name
		if content, ok := evData["content"].(map[string]any); ok {
			content["mediaUrl"] = mediaURL
		}
		s.mgr.broker.emitRaw(map[string]any{
			"type": "message-media", "sessionId": s.id, "id": msgID, "mediaUrl": mediaURL, "mediaType": kind,
		})
		s.mgr.hooks.dispatch(ctx, s.id, s.id, EvMessagesUpsert, evData)
	}
}

func (s *Session) handleReceiptEvent(evt *events.Receipt) {
	ctx := context.Background()
	status := ""
	switch evt.Type {
	case types.ReceiptTypeDelivered:
		status = "DELIVERED"
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		status = "READ"
	case types.ReceiptTypePlayed:
		status = "PLAYED"
	default:
		return
	}
	if err := s.mgr.msgs.updateMessageStatus(ctx, s.id, evt.MessageIDs, status); err != nil {
		return
	}
	s.mgr.broker.emitRaw(map[string]any{
		"type": "message-status", "sessionId": s.id, "ids": evt.MessageIDs, "status": status,
	})
	s.mgr.hooks.dispatch(ctx, s.id, s.id, EvMessagesUpdate, map[string]any{
		"ids": evt.MessageIDs, "status": status, "chat": evt.Chat.String(), "timestamp": evt.Timestamp.UnixMilli(),
	})
}

func (s *Session) handlePresenceEvent(evt *events.Presence) {
	s.mgr.broker.emitRaw(map[string]any{
		"type": "presence", "sessionId": s.id, "jid": evt.From.String(),
		"lastSeen": evt.LastSeen.UnixMilli(), "unavailable": evt.Unavailable,
	})
	s.mgr.hooks.dispatch(context.Background(), s.id, s.id, EvPresenceUpdate, map[string]any{
		"id": evt.From.String(),
		"presences": map[string]any{
			evt.From.String(): map[string]any{
				"lastKnownPresence": map[bool]string{true: "offline", false: "available"}[evt.Unavailable],
			},
		},
	})
}

func (s *Session) handleChatPresenceEvent(evt *events.ChatPresence) {
	s.mgr.broker.emitRaw(map[string]any{
		"type": "chat-presence", "sessionId": s.id, "jid": evt.Chat.String(),
		"sender": evt.Sender.String(), "state": string(evt.State),
	})
}

func (s *Session) sendTextMessage(ctx context.Context, peer types.JID, text string) (*whatsmeow.SendResponse, error) {
	resp, err := s.client.SendMessage(ctx, peer, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return nil, err
	}
	s.recordOutgoing(ctx, peer, resp, "text", text, "", "")
	return &resp, nil
}

func (s *Session) sendMediaMessage(ctx context.Context, peer types.JID, mediatype, mediaInput, caption, fileName string, ptt, gifPlayback bool, seconds uint32) (*whatsmeow.SendResponse, error) {
	data, mime, err := decodeMediaInput(mediaInput)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errMediaEmpty
	}
	var mt whatsmeow.MediaType
	switch mediatype {
	case "image":
		mt = whatsmeow.MediaImage
	case "video":
		mt = whatsmeow.MediaVideo
	case "audio":
		mt = whatsmeow.MediaAudio
	case "document":
		mt = whatsmeow.MediaDocument
	case "sticker":
		mt = whatsmeow.MediaImage
	default:
		return nil, errBadMediaType
	}
	if mime == "" {
		mime = defaultMimeForType(mediatype)
	}
	if ptt {
		// A voice note only plays from OGG/Opus; anything else reaches the
		// contact as a bubble that fails on playback.
		converted, convertedMime, convErr := voiceNoteAudio(ctx, data, mime)
		if convErr != nil {
			s.log.Warn("voice note conversion failed; sending as plain audio", "mime", mime, "err", convErr)
			ptt = false
		} else {
			data, mime = converted, convertedMime
		}
	}
	up, err := s.client.Upload(ctx, data, mt)
	if err != nil {
		return nil, err
	}
	msg := &waE2E.Message{}
	fileLen := up.FileLength
	if fileLen == 0 {
		fileLen = uint64(len(data))
	}
	switch mediatype {
	case "image":
		msg.ImageMessage = &waE2E.ImageMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mime),
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(fileLen),
		}
	case "video":
		msg.VideoMessage = &waE2E.VideoMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mime), GifPlayback: proto.Bool(gifPlayback),
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(fileLen),
		}
	case "audio":
		audio := &waE2E.AudioMessage{
			Mimetype: proto.String(mime), PTT: proto.Bool(ptt),
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(fileLen),
		}
		// Without the duration WhatsApp renders the voice note as 0:00.
		if seconds > 0 {
			audio.Seconds = proto.Uint32(seconds)
		}
		msg.AudioMessage = audio
	case "document":
		if fileName == "" {
			fileName = "document." + extForMime(mime)
		}
		msg.DocumentMessage = &waE2E.DocumentMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mime), FileName: proto.String(fileName),
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(fileLen),
		}
	case "sticker":
		msg.StickerMessage = &waE2E.StickerMessage{
			Mimetype: proto.String(mime),
			URL:      proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(fileLen),
		}
	}
	resp, err := s.client.SendMessage(ctx, peer, msg)
	if err != nil {
		return nil, err
	}
	text := caption
	if text == "" {
		text = "[" + mediatype + "]"
	}
	msgType := mediatype
	if ptt {
		msgType = "ptt"
	}
	s.recordOutgoingMedia(ctx, peer, &resp, msgType, text, fileName, mime, data)
	return &resp, nil
}

func (s *Session) recordOutgoing(ctx context.Context, peer types.JID, resp whatsmeow.SendResponse, msgType, text, fileName, mime string) {
	stored := &StoredMessage{
		ID:        string(resp.ID),
		SessionID: s.id,
		ChatJID:   peer.String(),
		SenderJID: s.jidString(),
		FromMe:    true,
		Type:      msgType,
		Text:      text,
		FileName:  fileName,
		MimeType:  mime,
		Status:    "PENDING",
		Timestamp: resp.Timestamp.UnixMilli(),
	}
	if stored.Timestamp == 0 {
		stored.Timestamp = time.Now().UnixMilli()
	}
	if err := s.mgr.msgs.insertMessage(ctx, stored); err != nil {
		s.log.Warn("persist outgoing message failed", "err", err)
	}
	evData := map[string]any{
		"key": map[string]any{
			"remoteJid": stored.ChatJID, "fromMe": true, "id": stored.ID,
		},
		"messageType":      stored.Type,
		"messageTimestamp": stored.Timestamp / 1000,
		"status":           stored.Status,
		"content": map[string]any{
			"type": stored.Type, "text": stored.Text,
			"fileName": stored.FileName, "mimeType": stored.MimeType,
		},
	}
	s.mgr.hooks.dispatch(ctx, s.id, s.id, EvSendMessage, evData)
	s.mgr.broker.emitMessage(stored)
}

func (s *Session) recordOutgoingMedia(ctx context.Context, peer types.JID, resp *whatsmeow.SendResponse, msgType, text, fileName, mime string, data []byte) {
	ext := extForMime(mime)
	name := ""
	if len(data) > 0 {
		if n, err := s.mgr.media.save(s.id, string(resp.ID), ext, data); err == nil {
			name = n
		}
	}
	stored := &StoredMessage{
		ID:        string(resp.ID),
		SessionID: s.id,
		ChatJID:   peer.String(),
		SenderJID: s.jidString(),
		FromMe:    true,
		Type:      msgType,
		Text:      text,
		FileName:  fileName,
		MimeType:  mime,
		Status:    "PENDING",
		Timestamp: resp.Timestamp.UnixMilli(),
	}
	if stored.Timestamp == 0 {
		stored.Timestamp = time.Now().UnixMilli()
	}
	if name != "" {
		stored.MediaURL = "/api/media/" + s.id + "/" + name
	}
	if err := s.mgr.msgs.insertMessage(ctx, stored); err != nil {
		s.log.Warn("persist outgoing media failed", "err", err)
	}
	s.mgr.hooks.dispatch(ctx, s.id, s.id, EvSendMessage, map[string]any{
		"key":              map[string]any{"remoteJid": stored.ChatJID, "fromMe": true, "id": stored.ID},
		"messageType":      stored.Type,
		"messageTimestamp": stored.Timestamp / 1000,
		"status":           stored.Status,
		"content": map[string]any{
			"type": stored.Type, "text": stored.Text, "fileName": stored.FileName,
			"mimeType": stored.MimeType, "mediaUrl": stored.MediaURL,
		},
	})
	s.mgr.broker.emitMessage(stored)
}

func (s *Session) jidString() string {
	if id := s.client.Store.ID; id != nil {
		return id.String()
	}
	return ""
}

func defaultMimeForType(t string) string {
	switch t {
	case "image":
		return "image/jpeg"
	case "video":
		return "video/mp4"
	case "audio":
		return "audio/ogg; codecs=opus"
	case "sticker":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

var (
	errMediaEmpty   = &apiError{status: 400, code: "BAD_REQUEST", message: "media is required (base64 or url)"}
	errBadMediaType = &apiError{status: 400, code: "BAD_REQUEST", message: "invalid mediatype (image|video|audio|document|sticker)"}
)

type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }
