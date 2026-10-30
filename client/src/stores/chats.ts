import { create } from "zustand";
import { eventStream } from "@/lib/event-stream";
import { listChats, listMessages, markChatRead } from "@/services/chats";
import type { ChatMessage, ChatSummary } from "@/types/chat";

type State = {
  chats: Record<string, ChatSummary[]>;
  messages: Record<string, ChatMessage[]>;
  activeChat: Record<string, string | null>;
};

export const useChats = create<State>(() => ({ chats: {}, messages: {}, activeChat: {} }));

export const setActiveChat = (sid: string, jid: string | null): void =>
  useChats.setState((s) => ({ activeChat: { ...s.activeChat, [sid]: jid } }));

export const refreshChats = async (sid: string): Promise<void> => {
  try {
    const chats = await listChats(sid);
    useChats.setState((s) => ({ chats: { ...s.chats, [sid]: chats } }));
  } catch {}
};

export const loadMessages = async (sid: string, chat: string): Promise<void> => {
  try {
    const msgs = await listMessages(sid, chat);
    // The API answers newest-first, but the thread renders top-down and anchors
    // its scroll at the bottom, so oldest must come first here — the same order
    // the live "message" handler below settles on.
    msgs.sort((a, b) => a.timestamp - b.timestamp);
    useChats.setState((s) => ({ messages: { ...s.messages, [chat]: msgs } }));
  } catch {}
};

export const openChat = async (sid: string, chat: string): Promise<void> => {
  setActiveChat(sid, chat);
  await Promise.all([loadMessages(sid, chat), markChatRead(sid, chat).then(() => refreshChats(sid)).catch(() => {})]);
};

const upsertChat = (chats: ChatSummary[], chat: ChatSummary): ChatSummary[] => {
  const idx = chats.findIndex((c) => c.jid === chat.jid);
  if (idx < 0) return [chat, ...chats];
  const next = [...chats];
  next[idx] = { ...next[idx], ...chat };
  return next;
};

let wired = false;
export const ensureChatsWired = (): void => {
  if (wired) return;
  wired = true;

  eventStream.on((ev) => {
    if (ev.type === "message" && ev.message) {
      const m = ev.message;
      useChats.setState((s) => {
        const existing = s.messages[m.chatJid] ?? [];
        const idx = existing.findIndex((x) => x.id === m.id);
        const messages = idx >= 0 ? existing.map((x, i) => (i === idx ? m : x)) : [...existing, m];
        messages.sort((a, b) => a.timestamp - b.timestamp);
        const prevChats = s.chats[m.sessionId] ?? [];
        const current: ChatSummary = prevChats.find((c) => c.jid === m.chatJid) ?? {
          sessionId: m.sessionId,
          jid: m.chatJid,
          name: m.pushName || m.chatJid.split("@")[0],
          isGroup: m.chatJid.includes("@g.us") || m.chatJid.includes("@newsletter"),
          unread: 0,
          lastText: "",
          lastType: m.type,
          lastFromMe: m.fromMe,
          lastAt: m.timestamp,
        };
        const unread = m.fromMe || s.activeChat[m.sessionId] === m.chatJid ? current.unread : current.unread + 1;
        const updated: ChatSummary = {
          ...current,
          unread,
          lastText: m.text || (m.type === "text" ? "" : `[${m.type}]`),
          lastType: m.type,
          lastFromMe: m.fromMe,
          lastAt: m.timestamp,
        };
        return {
          ...s,
          messages: { ...s.messages, [m.chatJid]: messages },
          chats: { ...s.chats, [m.sessionId]: upsertChat(prevChats, updated).sort((a, b) => b.lastAt - a.lastAt) },
        };
      });
    } else if (ev.type === "message-status" && ev.ids) {
      useChats.setState((s) => {
        const messages = { ...s.messages };
        for (const [chat, msgs] of Object.entries(messages)) {
          if (msgs.some((m) => ev.ids!.includes(m.id))) {
            messages[chat] = msgs.map((m) => (ev.ids!.includes(m.id) ? { ...m, status: ev.status } : m));
          }
        }
        return { ...s, messages };
      });
    } else if (ev.type === "message-media" && ev.mediaUrl) {
      useChats.setState((s) => {
        const messages = { ...s.messages };
        for (const [chat, msgs] of Object.entries(messages)) {
          if (msgs.some((m) => m.id === ev.id)) {
            messages[chat] = msgs.map((m) => (m.id === ev.id ? { ...m, mediaUrl: ev.mediaUrl } : m));
          }
        }
        return { ...s, messages };
      });
    }
  });
};
