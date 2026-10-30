import { useEffect } from "react";
import { MessageCircle, Phone, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { ChatList } from "@/components/domain/chat/ChatList";
import { ChatThread } from "@/components/domain/chat/ChatThread";
import { ChatComposer } from "@/components/domain/chat/ChatComposer";
import { EmptyState } from "@/components/shared/EmptyState";
import { Button } from "@/components/ui/button";
import { useStartCall } from "@/hooks/useStartCall";
import { ensureChatsWired, openChat, refreshChats, useChats } from "@/stores/chats";
import { useDevices } from "@/stores/devices";
import { sendContact, sendMedia, sendText } from "@/services/chats";
import { formatJidPhone } from "@/utils/phone";
import type { ChatMessage, ChatSummary } from "@/types/chat";

const EMPTY_CHATS: ChatSummary[] = [];
const EMPTY_MESSAGES: ChatMessage[] = [];

export const ChatsPage = ({ sid }: { sid: string }) => {
  const chats = useChats((s) => s.chats[sid] ?? EMPTY_CHATS);
  const activeJid = useChats((s) => s.activeChat[sid] ?? null);
  const messages = useChats((s) =>
    activeJid ? (s.messages[activeJid] ?? EMPTY_MESSAGES) : EMPTY_MESSAGES,
  );

  useEffect(() => {
    ensureChatsWired();
    void refreshChats(sid);
  }, [sid]);

  const active = chats.find((c) => c.jid === activeJid) ?? null;
  const micId = useDevices((s) => s.micId);
  const startCall = useStartCall(sid, micId);

  // Send the full JID: the server resolves LID chats to the real phone number,
  // so a call placed from a @lid chat does not target a bogus address.
  const callLead = () => {
    if (!activeJid) return;
    startCall.mutate(
      { phone: activeJid, record: true },
      { onSuccess: () => toast.success("Ligação iniciada. Acompanhe na aba Calls.") },
    );
  };

  const callButton =
    activeJid && !active?.isGroup && !activeJid.includes("@g.us") ? (
      <Button size="sm" onClick={callLead} disabled={startCall.isPending} aria-label="Ligar para o lead">
        {startCall.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Phone className="h-4 w-4" />}
        Ligar
      </Button>
    ) : null;

  return (
    <div className="grid h-[calc(100vh-8rem)] grid-cols-1 overflow-hidden rounded-lg border md:grid-cols-[20rem_1fr]">
      <div className="min-h-0">
        <ChatList
          chats={chats}
          activeJid={activeJid}
          onOpen={(jid) => void openChat(sid, jid)}
        />
      </div>
      <div className="min-h-0">
        {activeJid ? (
          <div className="flex h-full flex-col">
            <div className="min-h-0 flex-1">
              <ChatThread
                messages={messages}
                title={active?.name || activeJid.split("@")[0]}
                subtitle={formatJidPhone(activeJid)}
                headerAction={callButton}
              />
            </div>
            <ChatComposer
              disabled={false}
              onSendText={async (text) => {
                await sendText(sid, activeJid, text);
              }}
              onSendMedia={async (mediatype, media, caption, fileName, opts) => {
                await sendMedia(sid, activeJid, mediatype, media, caption, fileName, opts);
              }}
              onSendContact={async (name, phone) => {
                await sendContact(sid, activeJid, name, phone);
              }}
            />
          </div>
        ) : (
          <div className="flex h-full items-center justify-center">
            <EmptyState
              icon={<MessageCircle className="h-6 w-6" />}
              title="Select a chat"
              description="Choose a conversation from the list or open a new one by number."
            />
          </div>
        )}
      </div>
    </div>
  );
};
