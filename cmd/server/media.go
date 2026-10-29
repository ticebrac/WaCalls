package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

type mediaStore struct{ root string }

func newMediaStore(root string) (*mediaStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &mediaStore{root: root}, nil
}

func (m *mediaStore) save(sessionID, msgID, ext string, data []byte) (string, error) {
	dir := filepath.Join(m.root, sanitizeSegment(sessionID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	name := sanitizeSegment(msgID) + "-" + hex.EncodeToString(sum[:4]) + "." + ext
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

func (m *mediaStore) path(sessionID, name string) (string, bool) {
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	full := filepath.Join(m.root, sanitizeSegment(sessionID), name)
	if _, err := os.Stat(full); err != nil {
		return "", false
	}
	return full, true
}

func sanitizeSegment(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

func extForMime(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		switch mime {
		case "image/jpeg":
			return "jpg"
		case "image/webp":
			return "webp"
		case "image/gif":
			return "gif"
		default:
			return "png"
		}
	case strings.HasPrefix(mime, "video/"):
		if mime == "video/3gpp" {
			return "3gp"
		}
		return "mp4"
	case strings.HasPrefix(mime, "audio/"):
		switch mime {
		case "audio/ogg", "audio/opus":
			return "ogg"
		case "audio/mpeg":
			return "mp3"
		case "audio/mp4":
			return "m4a"
		default:
			return "opus"
		}
	case strings.HasPrefix(mime, "application/pdf"):
		return "pdf"
	default:
		return "bin"
	}
}

func decodeMediaInput(input string) ([]byte, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, "", fmt.Errorf("media is empty")
	}
	if strings.HasPrefix(input, "data:") {
		idx := strings.Index(input, ";base64,")
		if idx < 0 {
			return nil, "", fmt.Errorf("invalid data uri")
		}
		mime := strings.TrimPrefix(input[5:idx], "")
		data, err := base64.StdEncoding.DecodeString(input[idx+len(";base64,"):])
		return data, mime, err
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		client := &http.Client{}
		resp, err := client.Get(input)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("download failed: %s", resp.Status)
		}
		buf := make([]byte, 0, resp.ContentLength)
		tmp := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
			if len(buf) > 64<<20 {
				return nil, "", fmt.Errorf("media too large")
			}
		}
		mime := resp.Header.Get("Content-Type")
		if idx := strings.Index(mime, ";"); idx >= 0 {
			mime = mime[:idx]
		}
		return buf, mime, nil
	}
	data, err := base64.StdEncoding.DecodeString(input)
	return data, "", err
}

func jidDisplay(jid string) string {
	j, err := types.ParseJID(jid)
	if err != nil {
		if i := strings.Index(jid, "@"); i > 0 {
			return jid[:i]
		}
		return jid
	}
	return j.User
}

func isGroupJID(jid string) bool {
	j, err := types.ParseJID(jid)
	if err != nil {
		return strings.Contains(jid, "@g.us") || strings.Contains(jid, "@newsletter")
	}
	return j.Server == types.GroupServer || j.Server == types.NewsletterServer
}

func isUserJID(jid string) bool {
	j, err := types.ParseJID(jid)
	if err != nil {
		return strings.Contains(jid, "@s.whatsapp.net") || strings.Contains(jid, "@lid")
	}
	return j.Server == types.DefaultUserServer || j.Server == types.HiddenUserServer
}

func userJID(phone string) types.JID {
	return types.NewJID(normalizePhone(phone), types.DefaultUserServer)
}

func chatJIDFromInput(s string) types.JID {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "@") {
		j, err := types.ParseJID(s)
		if err == nil {
			return j
		}
	}
	if isGroupJID(s) {
		return types.NewJID(strings.TrimSuffix(strings.TrimPrefix(s, "https://chat.whatsapp.com/"), "@g.us"), types.GroupServer)
	}
	return userJID(s)
}
