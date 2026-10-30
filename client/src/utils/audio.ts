import { Mp3Encoder } from "@breezystack/lamejs";

const BLOCK = 1152;
const BITRATE_KBPS = 96;

// Browsers record in webm/ogg, which WhatsApp does not reliably play as a voice note.
// Decode the recording and re-encode it as MP3, which WhatsApp accepts everywhere.
export const recordingToMp3DataUrl = async (blob: Blob): Promise<string> => {
  const ctx = new AudioContext();
  let decoded: AudioBuffer;
  try {
    decoded = await ctx.decodeAudioData(await blob.arrayBuffer());
  } finally {
    void ctx.close();
  }

  const mono = decoded.getChannelData(0);
  const pcm = new Int16Array(mono.length);
  for (let i = 0; i < mono.length; i++) {
    const s = Math.max(-1, Math.min(1, mono[i]));
    pcm[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
  }

  const encoder = new Mp3Encoder(1, decoded.sampleRate, BITRATE_KBPS);
  const chunks: Uint8Array[] = [];
  for (let i = 0; i < pcm.length; i += BLOCK) {
    const out = encoder.encodeBuffer(pcm.subarray(i, i + BLOCK));
    if (out.length) chunks.push(out);
  }
  const tail = encoder.flush();
  if (tail.length) chunks.push(tail);

  const mp3 = new Blob(chunks as BlobPart[], { type: "audio/mpeg" });
  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(mp3);
  });
};

export const pickRecorderMime = (): string | undefined => {
  const candidates = ["audio/webm;codecs=opus", "audio/ogg;codecs=opus", "audio/webm"];
  return candidates.find((m) => typeof MediaRecorder !== "undefined" && MediaRecorder.isTypeSupported(m));
};

// WhatsApp renders a voice note without its duration as 0:00, so the length is
// read from the encoded file before sending. Returns 0 when it cannot be read.
export const audioDurationSeconds = (dataUrl: string): Promise<number> =>
  new Promise((resolve) => {
    const audio = new Audio();
    const done = (seconds: number) => resolve(Number.isFinite(seconds) && seconds > 0 ? Math.round(seconds) : 0);
    const timer = setTimeout(() => done(0), 5000);
    audio.onloadedmetadata = () => {
      clearTimeout(timer);
      done(audio.duration);
    };
    audio.onerror = () => {
      clearTimeout(timer);
      resolve(0);
    };
    audio.src = dataUrl;
  });
