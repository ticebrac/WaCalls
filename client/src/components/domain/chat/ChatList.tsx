import { useState } from "react";
import { MessageCircle, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { EmptyState } from "@/components/shared/EmptyState";
import { cn } from "@/lib/utils";
import type { ChatSummary } from "@/types/chat";

const formatTime = (ts: number): string => {
  if (!ts) return "";
  const d = new Date(ts);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};

const previewOf = (c: ChatSummary): string => {
  if (c.lastType === "text") return c.lastText;
  if (!c.lastText) return c.lastType ? `[${c.lastType}]` : "";
  return c.lastText;
};

export const ChatList = ({
  chats,
  activeJid,
  onOpen,
}: {
  chats: ChatSummary[];
  activeJid: string | null;
  onOpen: (jid: string) => void;
}) => {
  const [filter, setFilter] = useState("");
  const [newNumber, setNewNumber] = useState("");

  const filtered = filter
    ? chats.filter((c) => c.name.toLowerCase().includes(filter.toLowerCase()) || c.jid.includes(filter))
    : chats;

  const startNew = () => {
    const digits = newNumber.replace(/\D/g, "");
    if (!digits) return;
    onOpen(`${digits}@s.whatsapp.net`);
    setNewNumber("");
  };

  return (
    <div className="flex h-full flex-col border-r">
      <div className="space-y-2 border-b p-3">
        <div className="relative">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Search chats"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="pl-8"
          />
        </div>
        <div className="flex gap-2">
          <Input
            placeholder="New chat: number"
            value={newNumber}
            onChange={(e) => setNewNumber(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && startNew()}
            className="text-sm"
          />
          <Button size="sm" onClick={startNew} disabled={!newNumber.replace(/\D/g, "")}>
            Open
          </Button>
        </div>
      </div>
      <div className="flex-1 overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="p-4">
            <EmptyState
              icon={<MessageCircle className="h-6 w-6" />}
              title="No chats yet"
              description="Messages you receive will appear here, or open a chat by number above."
            />
          </div>
        ) : (
          filtered.map((c) => (
            <button
              key={c.jid}
              onClick={() => onOpen(c.jid)}
              className={cn(
                "flex w-full items-start gap-3 border-b px-3 py-3 text-left transition-colors hover:bg-muted/50",
                activeJid === c.jid && "bg-muted",
              )}
            >
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-primary/10 text-sm font-medium text-primary">
                {c.name.slice(0, 2).toUpperCase()}
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex items-center justify-between gap-2">
                  <span className="truncate text-sm font-medium">
                    {c.name || c.jid.split("@")[0]}
                    {c.isGroup && <span className="ml-1 text-xs text-muted-foreground">(group)</span>}
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">{formatTime(c.lastAt)}</span>
                </span>
                <span className="mt-0.5 flex items-center justify-between gap-2">
                  <span className={cn("truncate text-xs", c.lastFromMe && "text-muted-foreground")}>
                    {c.lastFromMe && !c.lastText && c.lastType !== "text" ? "You: " : c.lastFromMe ? "You: " : ""}
                    {previewOf(c)}
                  </span>
                  {c.unread > 0 && (
                    <span className="shrink-0 rounded-full bg-primary px-1.5 py-0.5 text-[10px] font-medium text-primary-foreground">
                      {c.unread}
                    </span>
                  )}
                </span>
              </span>
            </button>
          ))
        )}
      </div>
    </div>
  );
};
