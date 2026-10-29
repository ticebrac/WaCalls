export type ChatSummary = {
  sessionId: string;
  jid: string;
  name: string;
  isGroup: boolean;
  unread: number;
  lastText: string;
  lastType: string;
  lastFromMe: boolean;
  lastAt: number;
};

export type ChatMessage = {
  id: string;
  sessionId: string;
  chatJid: string;
  senderJid: string;
  fromMe: boolean;
  type: string;
  text: string;
  fileName?: string;
  mimeType?: string;
  mediaUrl?: string;
  status: string;
  timestamp: number;
  pushName?: string;
};

export type SendTextResult = { id: string; timestamp: number };

export type SendMediaResult = { id: string; timestamp: number };
