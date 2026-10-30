package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestCallRecorderMixesStereoWAV(t *testing.T) {
	dir := t.TempDir()
	r, err := newCallRecorder(dir, "call/1", CallRecord{})
	if err != nil {
		t.Fatal(err)
	}
	mic := make([]float32, 1600) // 100 ms
	for i := range mic {
		mic[i] = 0.5
	}
	r.write(0, mic)
	r.write(1, mic[:800]) // peer leg is shorter: must be padded with silence

	name, durMs, err := r.finish()
	if err != nil {
		t.Fatal(err)
	}
	if name != "call_1.wav" {
		t.Fatalf("name = %q", name)
	}
	// Legs are aligned to wall-clock time, so allow a few ms of scheduling slack.
	if durMs < 100 || durMs > 150 {
		t.Fatalf("duration = %dms, want ~100", durMs)
	}

	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatal("not a RIFF/WAVE file")
	}
	if ch := binary.LittleEndian.Uint16(data[22:24]); ch != 2 {
		t.Fatalf("channels = %d, want 2", ch)
	}
	if rate := binary.LittleEndian.Uint32(data[24:28]); rate != 16000 {
		t.Fatalf("rate = %d, want 16000", rate)
	}
	frames := int64(len(data)-44) / 4
	if frames*1000/16000 != durMs {
		t.Fatalf("frames = %d does not match reported duration %dms", frames, durMs)
	}
	// Each leg's loud samples must survive intact; the wall-clock offset only
	// shifts them, so count non-zero samples per channel.
	var nonZeroLeft, nonZeroRight int
	for f := 0; f < int(frames); f++ {
		if binary.LittleEndian.Uint16(data[44+f*4:]) != 0 {
			nonZeroLeft++
		}
		if binary.LittleEndian.Uint16(data[44+f*4+2:]) != 0 {
			nonZeroRight++
		}
	}
	if nonZeroLeft != 1600 || nonZeroRight != 800 {
		t.Fatalf("non-zero samples left=%d right=%d, want 1600 and 800", nonZeroLeft, nonZeroRight)
	}

	if _, err := os.Stat(filepath.Join(dir, "call_1.mic.raw")); !os.IsNotExist(err) {
		t.Fatal("raw leg file should be removed")
	}
}

func TestCallRecorderWithoutAudioProducesNoFile(t *testing.T) {
	dir := t.TempDir()
	r, err := newCallRecorder(dir, "c2", CallRecord{})
	if err != nil {
		t.Fatal(err)
	}
	name, durMs, err := r.finish()
	if err != nil || name != "" || durMs != 0 {
		t.Fatalf("finish() = (%q, %d, %v), want empty", name, durMs, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected empty dir, got %d entries", len(entries))
	}
}

func TestWithDefaultCountry(t *testing.T) {
	cases := map[string]string{
		"11999999999":   "5511999999999",
		"1133334444":    "551133334444",
		"5511999999999": "5511999999999",
	}
	for in, want := range cases {
		if got := withDefaultCountry(in); got != want {
			t.Errorf("withDefaultCountry(%q) = %q, want %q", in, got, want)
		}
	}
}
