import { apiGet, apiPost } from "@/lib/api";
import type { ChatMessage, ChatSummary, SendMediaResult, SendTextResult } from "@/types/chat";

export const listChats = async (sid: string): Promise<ChatSummary[]> => {
  const r = await apiGet<{ chats: ChatSummary[] }>(`/api/sessions/${sid}/chats`);
  return r.chats ?? [];
};

export const listMessages = async (sid: string, chat: string, before?: number): Promise<ChatMessage[]> => {
  const params = new URLSearchParams({ chat });
  if (before) params.set("before", String(before));
  const r = await apiGet<{ messages: ChatMessage[] }>(`/api/sessions/${sid}/messages?${params}`);
  return r.messages ?? [];
};

export const sendText = async (sid: string, chatJid: string, text: string): Promise<SendTextResult> =>
  apiPost<SendTextResult>(`/api/sessions/${sid}/messages`, { chatJid, text });

export const sendMedia = async (
  sid: string,
  chatJid: string,
  mediatype: string,
  media: string,
  caption: string,
  fileName?: string,
): Promise<SendMediaResult> =>
  apiPost<SendMediaResult>(`/api/sessions/${sid}/messages/media`, { chatJid, mediatype, media, caption, fileName });

export const markChatRead = async (sid: string, chatJid: string): Promise<void> => {
  await apiPost(`/api/sessions/${sid}/chats/read`, { chatJid });
};
