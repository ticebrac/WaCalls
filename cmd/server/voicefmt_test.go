package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsOggOpus(t *testing.T) {
	cases := map[string]bool{
		"audio/ogg; codecs=opus": true,
		"audio/ogg":              false,
		"audio/mpeg":             false,
		"audio/webm;codecs=opus": false,
		"":                       false,
	}
	for mime, want := range cases {
		if got := isOggOpus(mime); got != want {
			t.Errorf("isOggOpus(%q) = %v, want %v", mime, got, want)
		}
	}
}

// An already-valid voice note must pass through untouched (and without
// requiring ffmpeg).
func TestVoiceNoteAudioKeepsOpus(t *testing.T) {
	original := []byte("OggS-not-really-but-untouched")
	got, mime, err := voiceNoteAudio(context.Background(), original, "audio/ogg; codecs=opus")
	if err != nil {
		t.Fatalf("voiceNoteAudio: %v", err)
	}
	if mime != "audio/ogg; codecs=opus" {
		t.Errorf("mime = %q", mime)
	}
	if !bytes.Equal(got, original) {
		t.Error("already-valid opus input was rewritten")
	}
}

// WhatsApp refuses to play a PTT message that is not OGG/Opus, so the MP3 the
// browser records has to come back as an Ogg stream.
func TestVoiceNoteAudioReencodesMp3(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	mp3 := filepath.Join(dir, "in.mp3")
	gen := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-ac", "2", "-ar", "44100", mp3)
	if out, genErr := gen.CombinedOutput(); genErr != nil {
		t.Skipf("cannot generate the fixture: %v: %s", genErr, out)
	}
	data, err := os.ReadFile(mp3)
	if err != nil {
		t.Fatal(err)
	}

	ogg, mime, err := voiceNoteAudio(context.Background(), data, "audio/mpeg")
	if err != nil {
		t.Fatalf("voiceNoteAudio: %v", err)
	}
	if mime != "audio/ogg; codecs=opus" {
		t.Errorf("mime = %q, want audio/ogg; codecs=opus", mime)
	}
	if !bytes.HasPrefix(ogg, []byte("OggS")) {
		t.Error("converted output is not an Ogg stream")
	}
}
