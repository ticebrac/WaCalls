import { useEffect } from "react";
import { MessageCircle } from "lucide-react";
import { ChatList } from "@/components/domain/chat/ChatList";
import { ChatThread } from "@/components/domain/chat/ChatThread";
import { ChatComposer } from "@/components/domain/chat/ChatComposer";
import { EmptyState } from "@/components/shared/EmptyState";
import { ensureChatsWired, openChat, refreshChats, useChats } from "@/stores/chats";
import { sendMedia, sendText } from "@/services/chats";

export const ChatsPage = ({ sid }: { sid: string }) => {
  const chats = useChats((s) => s.chats[sid] ?? []);
  const activeJid = useChats((s) => s.activeChat[sid] ?? null);
  const messages = useChats((s) => (activeJid ? (s.messages[activeJid] ?? []) : []));

  useEffect(() => {
    ensureChatsWired();
    void refreshChats(sid);
  }, [sid]);

  const active = chats.find((c) => c.jid === activeJid) ?? null;

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
                subtitle={activeJid}
              />
            </div>
            <ChatComposer
              disabled={false}
              onSendText={async (text) => {
                await sendText(sid, activeJid, text);
              }}
              onSendMedia={async (mediatype, media, caption, fileName) => {
                await sendMedia(sid, activeJid, mediatype, media, caption, fileName);
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
