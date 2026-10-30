package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"wacalls/internal/voip/media"
)

const (
	recordingRate     = 16000
	recordingChannels = 2
	recordingBlock    = 4096
)

// callRecorder captures both legs of a call as a stereo 16-bit WAV:
// channel 0 = operator microphone (browser), channel 1 = remote peer.
// Each leg is appended to its own raw file, padded with silence to the
// wall-clock time it arrived at, and interleaved into the WAV on finish.
type callRecorder struct {
	mu     sync.Mutex
	start  time.Time
	dir    string
	base   string
	paths  [2]string
	raw    [2]*os.File
	frames [2]int64
	meta   CallRecord
	done   bool
}

func newCallRecorder(dir, callID string, meta CallRecord) (*callRecorder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	r := &callRecorder{start: time.Now(), dir: dir, base: sanitizeSegment(callID), meta: meta}
	for i, suffix := range []string{"mic", "peer"} {
		r.paths[i] = filepath.Join(dir, r.base+"."+suffix+".raw")
		f, err := os.Create(r.paths[i])
		if err != nil {
			r.closeRaw()
			r.removeRaw()
			return nil, err
		}
		r.raw[i] = f
	}
	return r, nil
}

// write appends 16 kHz mono PCM for one channel. It is nil-safe so callers
// need not check whether recording is enabled for the call.
func (r *callRecorder) write(ch int, pcm []float32) {
	if r == nil || len(pcm) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	due := int64(time.Since(r.start).Seconds() * recordingRate)
	if due > r.frames[ch] {
		if err := r.padTo(ch, due); err != nil {
			return
		}
	}
	if _, err := r.raw[ch].Write(media.PCMFloat32ToInt16LE(pcm)); err == nil {
		r.frames[ch] += int64(len(pcm))
	}
}

// finish closes the recording. It returns the WAV file name and its duration
// in milliseconds, or an empty name when no audio was captured.
func (r *callRecorder) finish() (string, int64, error) {
	if r == nil {
		return "", 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return "", 0, nil
	}
	r.done = true

	total := max(r.frames[0], r.frames[1])
	if total == 0 {
		r.closeRaw()
		r.removeRaw()
		return "", 0, nil
	}
	for ch := range 2 {
		if err := r.padTo(ch, total); err != nil {
			r.closeRaw()
			r.removeRaw()
			return "", 0, err
		}
	}
	r.closeRaw()

	name := r.base + ".wav"
	err := r.mixWAV(filepath.Join(r.dir, name), total)
	r.removeRaw()
	if err != nil {
		_ = os.Remove(filepath.Join(r.dir, name))
		return "", 0, err
	}
	return name, total * 1000 / recordingRate, nil
}

func (r *callRecorder) padTo(ch int, target int64) error {
	zeros := make([]byte, recordingBlock*2)
	for r.frames[ch] < target {
		n := min(target-r.frames[ch], recordingBlock)
		if _, err := r.raw[ch].Write(zeros[:int(n)*2]); err != nil {
			return err
		}
		r.frames[ch] += n
	}
	return nil
}

func (r *callRecorder) mixWAV(path string, frames int64) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Write(wavHeader(frames)); err != nil {
		return err
	}

	var readers [2]*bufio.Reader
	for i := range 2 {
		f, err := os.Open(r.paths[i])
		if err != nil {
			return err
		}
		defer f.Close()
		readers[i] = bufio.NewReaderSize(f, 64*1024)
	}

	a := make([]byte, recordingBlock*2)
	b := make([]byte, recordingBlock*2)
	o := make([]byte, recordingBlock*4)
	for remaining := frames; remaining > 0; {
		n := int(min(remaining, recordingBlock))
		if _, err := io.ReadFull(readers[0], a[:n*2]); err != nil {
			return err
		}
		if _, err := io.ReadFull(readers[1], b[:n*2]); err != nil {
			return err
		}
		for i := range n {
			copy(o[i*4:i*4+2], a[i*2:i*2+2])
			copy(o[i*4+2:i*4+4], b[i*2:i*2+2])
		}
		if _, err := out.Write(o[:n*4]); err != nil {
			return err
		}
		remaining -= int64(n)
	}
	return nil
}

func (r *callRecorder) closeRaw() {
	for i, f := range r.raw {
		if f != nil {
			_ = f.Close()
			r.raw[i] = nil
		}
	}
}

func (r *callRecorder) removeRaw() {
	for _, p := range r.paths {
		if p != "" {
			_ = os.Remove(p)
		}
	}
}

func wavHeader(frames int64) []byte {
	const bits = 16
	const blockAlign = recordingChannels * bits / 8
	dataSize := uint32(frames * blockAlign)
	h := make([]byte, 44)
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], 36+dataSize)
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16)
	binary.LittleEndian.PutUint16(h[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:24], recordingChannels)
	binary.LittleEndian.PutUint32(h[24:28], recordingRate)
	binary.LittleEndian.PutUint32(h[28:32], recordingRate*blockAlign)
	binary.LittleEndian.PutUint16(h[32:34], blockAlign)
	binary.LittleEndian.PutUint16(h[34:36], bits)
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], dataSize)
	return h
}
