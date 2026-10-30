import { useEffect, useRef, useState } from "react";
import { Contact, Image as ImageIcon, Mic, SendHorizonal, Smile, Square, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { toWhatsAppPhone } from "@/utils/phone";

// The MP3 encoder is loaded only when the user first records audio.
const loadAudio = () => import("@/utils/audio");

// Audio always goes out as a WhatsApp voice note (PTT); anything else renders
// as a music file with the attachment name, which is not what the contact expects.
const sendAudio = async (
  send: (mediatype: string, media: string, caption: string, fileName?: string, opts?: { ptt?: boolean; seconds?: number }) => Promise<void>,
  dataUrl: string,
  caption: string,
  fileName: string,
): Promise<void> => {
  const { audioDurationSeconds } = await loadAudio();
  const seconds = await audioDurationSeconds(dataUrl);
  await send("audio", dataUrl, caption, fileName, { ptt: true, seconds });
};

const EMOJIS = [
  "😀", "😂", "🤣", "😊", "😍", "😘", "😎", "🤔", "😅", "😭",
  "😡", "🙏", "👍", "👎", "👏", "🙌", "💪", "🤝", "❤️", "🔥",
  "✅", "❌", "⭐", "🎉", "📞", "📱", "💰", "📅", "🚀", "👋",
];

const mediaTypeOf = (mime: string): string => {
  if (mime.startsWith("image/")) return "image";
  if (mime.startsWith("video/")) return "video";
  if (mime.startsWith("audio/")) return "audio";
  return "document";
};

export const ChatComposer = ({
  disabled,
  onSendText,
  onSendMedia,
  onSendContact,
}: {
  disabled: boolean;
  onSendText: (text: string) => Promise<void>;
  onSendMedia: (
    mediatype: string,
    media: string,
    caption: string,
    fileName?: string,
    opts?: { ptt?: boolean; seconds?: number },
  ) => Promise<void>;
  onSendContact: (name: string, phone: string) => Promise<void>;
}) => {
  const [text, setText] = useState("");
  const [pending, setPending] = useState<{ file: File; dataUrl: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [showEmoji, setShowEmoji] = useState(false);
  const [contactOpen, setContactOpen] = useState(false);
  const [contactName, setContactName] = useState("");
  const [contactPhone, setContactPhone] = useState("");
  const [recording, setRecording] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);

  useEffect(
    () => () => {
      recorderRef.current?.stream.getTracks().forEach((t) => t.stop());
    },
    [],
  );

  const pickFile = (file: File) => {
    if (file.size > 64 * 1024 * 1024) return;
    const reader = new FileReader();
    reader.onload = () => setPending({ file, dataUrl: String(reader.result) });
    reader.readAsDataURL(file);
  };

  const insertEmoji = (emoji: string) => {
    setText((t) => t + emoji);
    inputRef.current?.focus();
  };

  const send = async () => {
    if (busy) return;
    if (pending) {
      setBusy(true);
      try {
        const type = mediaTypeOf(pending.file.type);
        if (type === "audio") {
          await sendAudio(onSendMedia, pending.dataUrl, text, pending.file.name);
        } else {
          await onSendMedia(type, pending.dataUrl, text, pending.file.name);
        }
        setPending(null);
        setText("");
      } finally {
        setBusy(false);
      }
      return;
    }
    const t = text.trim();
    if (!t) return;
    setBusy(true);
    try {
      await onSendText(t);
      setText("");
    } finally {
      setBusy(false);
    }
  };

  const startRecording = async () => {
    if (!navigator.mediaDevices?.getUserMedia) {
      toast.error("Gravação de áudio indisponível neste navegador.");
      return;
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const { pickRecorderMime } = await loadAudio();
      const mime = pickRecorderMime();
      const rec = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined);
      chunksRef.current = [];
      rec.ondataavailable = (e) => {
        if (e.data.size > 0) chunksRef.current.push(e.data);
      };
      rec.onstop = () => {
        stream.getTracks().forEach((t) => t.stop());
        void finishRecording();
      };
      rec.start();
      recorderRef.current = rec;
      setRecording(true);
    } catch {
      toast.error("Não foi possível acessar o microfone.");
    }
  };

  const finishRecording = async () => {
    setRecording(false);
    const blob = new Blob(chunksRef.current, { type: recorderRef.current?.mimeType || "audio/webm" });
    chunksRef.current = [];
    if (blob.size === 0) return;
    setBusy(true);
    try {
      const { recordingToMp3DataUrl } = await loadAudio();
      const dataUrl = await recordingToMp3DataUrl(blob);
      await sendAudio(onSendMedia, dataUrl, "", "audio.mp3");
    } catch (e) {
      toast.error((e as Error).message || "Falha ao enviar o áudio.");
    } finally {
      setBusy(false);
    }
  };

  const stopRecording = () => {
    if (recorderRef.current && recorderRef.current.state !== "inactive") {
      recorderRef.current.stop();
    }
  };

  const submitContact = async () => {
    const phone = toWhatsAppPhone(contactPhone);
    if (!contactName.trim() || !phone) return;
    setBusy(true);
    try {
      await onSendContact(contactName.trim(), phone);
      setContactOpen(false);
      setContactName("");
      setContactPhone("");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (recording) {
    return (
      <div className="flex items-center gap-3 border-t p-3">
        <span className="h-2.5 w-2.5 animate-pulse rounded-full bg-destructive" />
        <span className="flex-1 text-sm">Gravando áudio…</span>
        <Button size="sm" variant="destructive" onClick={stopRecording}>
          <Square className="h-4 w-4" />
          Parar e enviar
        </Button>
      </div>
    );
  }

  return (
    <div className="border-t p-3">
      {pending && (
        <div className="mb-2 flex items-center gap-2 rounded-md border bg-muted/50 px-3 py-2 text-sm">
          <span className="truncate flex-1">
            {pending.file.name} <span className="text-muted-foreground">({Math.round(pending.file.size / 1024)} KB)</span>
          </span>
          <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => setPending(null)}>
            <X className="h-4 w-4" />
          </Button>
        </div>
      )}
      {showEmoji && (
        <div className="mb-2 grid grid-cols-10 gap-1 rounded-md border bg-popover p-2">
          {EMOJIS.map((e) => (
            <button
              key={e}
              type="button"
              onClick={() => insertEmoji(e)}
              className="rounded text-xl hover:bg-muted"
              aria-label={`Inserir ${e}`}
            >
              {e}
            </button>
          ))}
        </div>
      )}
      <div className="flex items-center gap-2">
        <input
          ref={fileRef}
          type="file"
          className="hidden"
          accept="image/*,video/*,audio/*,application/*"
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) pickFile(f);
            e.target.value = "";
          }}
        />
        <Button variant="outline" size="icon" disabled={disabled} onClick={() => setShowEmoji((v) => !v)} aria-label="Emojis">
          <Smile className="h-4 w-4" />
        </Button>
        <Button
          variant="outline"
          size="icon"
          disabled={disabled}
          onClick={() => fileRef.current?.click()}
          aria-label="Anexar arquivo"
        >
          <ImageIcon className="h-4 w-4" />
        </Button>
        <Button variant="outline" size="icon" disabled={disabled} onClick={() => setContactOpen(true)} aria-label="Enviar contato">
          <Contact className="h-4 w-4" />
        </Button>
        <Input
          ref={inputRef}
          placeholder={pending ? "Adicione uma legenda…" : "Digite uma mensagem"}
          value={text}
          disabled={disabled}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void send();
            }
          }}
          className="flex-1"
        />
        {text.trim() || pending ? (
          <Button size="icon" disabled={disabled || busy} onClick={() => void send()} aria-label="Enviar">
            <SendHorizonal className="h-4 w-4" />
          </Button>
        ) : (
          <Button
            size="icon"
            variant="outline"
            disabled={disabled || busy}
            onClick={() => void startRecording()}
            aria-label="Gravar áudio"
          >
            <Mic className="h-4 w-4" />
          </Button>
        )}
      </div>

      <Dialog open={contactOpen} onOpenChange={(o) => !busy && setContactOpen(o)}>
        <DialogContent>
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              void submitContact();
            }}
          >
            <DialogHeader>
              <DialogTitle>Enviar contato</DialogTitle>
              <DialogDescription>Nome e número do contato. Sem DDI, usa +55.</DialogDescription>
            </DialogHeader>
            <Input placeholder="Nome" value={contactName} onChange={(e) => setContactName(e.target.value)} autoFocus />
            <Input
              placeholder="Telefone (ex.: 11 99999 8888)"
              inputMode="tel"
              value={contactPhone}
              onChange={(e) => setContactPhone(e.target.value)}
            />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setContactOpen(false)} disabled={busy}>
                Cancelar
              </Button>
              <Button type="submit" disabled={busy || !contactName.trim() || !toWhatsAppPhone(contactPhone)}>
                Enviar
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
};
