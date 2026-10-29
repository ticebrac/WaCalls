import { useRef, useState } from "react";
import { Image as ImageIcon, SendHorizonal, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

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
}: {
  disabled: boolean;
  onSendText: (text: string) => Promise<void>;
  onSendMedia: (mediatype: string, media: string, caption: string, fileName?: string) => Promise<void>;
}) => {
  const [text, setText] = useState("");
  const [pending, setPending] = useState<{ file: File; dataUrl: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const pickFile = (file: File) => {
    if (file.size > 64 * 1024 * 1024) return;
    const reader = new FileReader();
    reader.onload = () => setPending({ file, dataUrl: String(reader.result) });
    reader.readAsDataURL(file);
  };

  const send = async () => {
    if (busy) return;
    if (pending) {
      setBusy(true);
      try {
        await onSendMedia(mediaTypeOf(pending.file.type), pending.dataUrl, text, pending.file.name);
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
        <Button
          variant="outline"
          size="icon"
          disabled={disabled}
          onClick={() => fileRef.current?.click()}
          aria-label="Attach file"
        >
          <ImageIcon className="h-4 w-4" />
        </Button>
        <Input
          placeholder={pending ? "Add a caption…" : "Type a message"}
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
        <Button size="icon" disabled={disabled || busy || (!text.trim() && !pending)} onClick={() => void send()}>
          <SendHorizonal className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
};
