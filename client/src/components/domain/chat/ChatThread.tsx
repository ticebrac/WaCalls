import { useEffect, useRef } from "react";
import { FileText, Check, CheckCheck, Clock } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ChatMessage } from "@/types/chat";

const formatTime = (ts: number): string => {
  const d = new Date(ts);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};

const StatusIcon = ({ status }: { status: string }) => {
  switch (status) {
    case "PENDING":
      return <Clock className="h-3 w-3" />;
    case "DELIVERED":
      return <Check className="h-3 w-3" />;
    case "READ":
    case "PLAYED":
      return <CheckCheck className="h-3 w-3 text-sky-500" />;
    default:
      return <CheckCheck className="h-3 w-3" />;
  }
};

const BubbleContent = ({ m }: { m: ChatMessage }) => {
  if (m.mediaUrl) {
    switch (m.type) {
      case "image":
      case "sticker":
        return (
          <img src={m.mediaUrl} alt={m.text || "image"} className="max-h-64 max-w-full rounded-md" loading="lazy" />
        );
      case "video":
        return <video src={m.mediaUrl} controls className="max-h-64 max-w-full rounded-md" />;
      case "audio":
      case "ptt":
        return <audio src={m.mediaUrl} controls className="w-56" />;
      case "document":
        return (
          <a
            href={m.mediaUrl}
            target="_blank"
            rel="noreferrer"
            className="flex items-center gap-2 rounded-md bg-background/50 p-2 text-sm underline-offset-4 hover:underline"
          >
            <FileText className="h-4 w-4 shrink-0" />
            <span className="truncate">{m.fileName || "document"}</span>
          </a>
        );
    }
  }
  if (m.type === "text" || m.type === "unknown" || m.type === "") {
    return <span className="whitespace-pre-wrap break-words">{m.text}</span>;
  }
  return <span className="text-sm italic opacity-80">[{m.type}{m.text ? `: ${m.text}` : ""}]</span>;
};

export const ChatThread = ({
  messages,
  title,
  subtitle,
}: {
  messages: ChatMessage[];
  title: string;
  subtitle: string;
}) => {
  const bottomRef = useRef<HTMLDivElement>(null);
  const count = messages.length;

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [count]);

  return (
    <div className="flex h-full flex-col">
      <div className="border-b px-4 py-3">
        <div className="text-sm font-semibold">{title}</div>
        <div className="text-xs text-muted-foreground">{subtitle}</div>
      </div>
      <div className="flex-1 space-y-2 overflow-y-auto px-4 py-3">
        {messages.map((m) => (
          <div key={m.id} className={cn("flex", m.fromMe ? "justify-end" : "justify-start")}>
            <div
              className={cn(
                "max-w-[75%] rounded-lg px-3 py-2 text-sm shadow-sm",
                m.fromMe ? "bg-primary text-primary-foreground" : "bg-muted",
              )}
            >
              {!m.fromMe && m.pushName && (
                <div className="mb-0.5 text-[11px] font-medium opacity-70">{m.pushName}</div>
              )}
              <BubbleContent m={m} />
              <div
                className={cn(
                  "mt-1 flex items-center justify-end gap-1 text-[10px]",
                  m.fromMe ? "text-primary-foreground/70" : "text-muted-foreground",
                )}
              >
                {formatTime(m.timestamp)}
                {m.fromMe && <StatusIcon status={m.status} />}
              </div>
            </div>
          </div>
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  );
};
