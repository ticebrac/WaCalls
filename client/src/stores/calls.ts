import { create } from "zustand";
import { eventStream, type BrokerEvent } from "@/lib/event-stream";
import { getClientId } from "@/lib/client-id";
import { queryClient, queryKeys } from "@/lib/query";
import type { OpenCall } from "@/lib/webrtc";
import type { CallSummary, IncomingPayload } from "@/types/call";

type State = {
  calls: CallSummary[];
  ownConnections: Map<string, OpenCall>;
  incoming: IncomingPayload | null;
};

export const useCalls = create<State>(() => ({
  calls: [],
  ownConnections: new Map(),
  incoming: null,
}));

let wired = false;
export const ensureCallsWired = (): void => {
  if (wired) return;
  wired = true;
  eventStream.on((ev: BrokerEvent) => {
    if (ev.type === "call-list") {
      useCalls.setState({ calls: ev.calls });
    } else if (ev.type === "call-status") {
      useCalls.setState((s) => ({
        calls: s.calls.map((c) =>
          c.callId === ev.id
            ? { ...c, sessionId: ev.sessionId, status: ev.status, peer: ev.peer, startedAt: ev.startedAt }
            : c,
        ),
      }));
    } else if (ev.type === "call-ended") {
      useCalls.setState((s) => {
        const conn = s.ownConnections.get(ev.id);
        if (conn) conn.close();
        detachPeerAudio(ev.id);
        const next = new Map(s.ownConnections);
        next.delete(ev.id);
        return {
          calls: s.calls.filter((c) => c.callId !== ev.id),
          ownConnections: next,
          incoming: s.incoming?.callId === ev.id ? null : s.incoming,
        };
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.history });
    } else if (ev.type === "incoming") {
      useCalls.setState({ incoming: { sessionId: ev.sessionId, callId: ev.id, peer: ev.peer, offeredAt: ev.offeredAt } });
    } else if (ev.type === "incoming-claimed") {
      useCalls.setState((s) => (s.incoming?.callId === ev.id ? { incoming: null } : s));
    }
  });
};

export const isMine = (call: CallSummary): boolean => call.owner === getClientId();

// Peer playback lives outside React: the card that used to own the <audio>
// element mounts only on the Calls tab, so a call placed from a chat played
// nothing for the operator even though the server was forwarding the audio.
const peerAudio = new Map<string, HTMLAudioElement>();

export const attachPeerAudio = (id: string, stream: MediaStream | null): void => {
  if (!stream || typeof Audio === "undefined") return;
  let el = peerAudio.get(id);
  if (!el) {
    el = new Audio();
    el.autoplay = true;
    peerAudio.set(id, el);
  }
  el.srcObject = stream;
  void el.play().catch(() => {});
};

export const detachPeerAudio = (id: string): void => {
  const el = peerAudio.get(id);
  if (!el) return;
  peerAudio.delete(id);
  try {
    el.pause();
  } catch {}
  el.srcObject = null;
};

export const getPeerAudio = (id: string): HTMLAudioElement | undefined => peerAudio.get(id);

export const registerOwnConnection = (id: string, conn: OpenCall): void => {
  attachPeerAudio(id, conn.remoteStream);
  useCalls.setState((s) => {
    const next = new Map(s.ownConnections);
    next.set(id, conn);
    return { ownConnections: next };
  });
};

export const clearIncoming = (): void => useCalls.setState({ incoming: null });
