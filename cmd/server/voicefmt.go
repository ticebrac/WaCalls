package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WhatsApp only plays a voice note (PTT) from mono 48 kHz OGG/Opus. Sending an
// MP3 marked as PTT renders the voice-note bubble but fails on playback with
// "audio is not available because something is wrong with the audio file" on
// Android — see WhiskeySockets/Baileys#1797.
func isOggOpus(mime string) bool {
	m := strings.ToLower(mime)
	return strings.Contains(m, "audio/ogg") && strings.Contains(m, "opus")
}

// voiceNoteAudio re-encodes data into the OGG/Opus WhatsApp requires for PTT
// and returns it with the matching mime. It gives back an error when ffmpeg is
// unavailable or rejects the input so the caller can fall back to a plain
// audio message instead of failing the send.
func voiceNoteAudio(ctx context.Context, data []byte, mime string) ([]byte, string, error) {
	if isOggOpus(mime) {
		return data, mime, nil
	}
	ffmpeg := os.Getenv("FFMPEG_PATH")
	if ffmpeg == "" {
		path, err := exec.LookPath("ffmpeg")
		if err != nil {
			return nil, "", fmt.Errorf("ffmpeg not found in PATH: %w", err)
		}
		ffmpeg = path
	}

	dir, err := os.MkdirTemp("", "wacalls-voice-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)

	ext := extForMime(mime)
	if ext == "" || ext == "bin" {
		ext = "audio"
	}
	in := filepath.Join(dir, "in."+ext)
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, "", err
	}
	out := filepath.Join(dir, "out.ogg")

	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", in, "-map", "0:a:0", "-vn",
		"-ac", "1", "-ar", "48000", "-c:a", "libopus", "-b:a", "48k",
		"-f", "ogg", out,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, "", fmt.Errorf("ffmpeg: %s", msg)
	}
	ogg, err := os.ReadFile(out)
	if err != nil {
		return nil, "", err
	}
	if len(ogg) == 0 {
		return nil, "", fmt.Errorf("ffmpeg produced an empty file")
	}
	return ogg, "audio/ogg; codecs=opus", nil
}
