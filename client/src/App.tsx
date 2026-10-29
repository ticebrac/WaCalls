import { useEffect, useState } from "react";
import { MessageCircle, PhoneCall, PlusCircle } from "lucide-react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import { AppShell } from "@/components/layout/AppShell";
import { CallsPage } from "@/pages/CallsPage";
import { ChatsPage } from "@/pages/ChatsPage";
import { SessionPairing } from "@/components/domain/session/SessionPairing";
import { SessionHeader } from "@/components/domain/session/SessionHeader";
import { IncomingCallModal } from "@/components/domain/call/IncomingCallModal";
import { EmptyState } from "@/components/shared/EmptyState";
import { ensureSessionsWired, useSessions } from "@/stores/sessions";
import { ensureCallsWired } from "@/stores/calls";
import { useTheme } from "@/stores/theme";

type View = "chats" | "calls";

export const App = () => {
  const sessions = useSessions((s) => s.sessions);
  const activeId = useSessions((s) => s.activeId);
  const theme = useTheme((s) => s.theme);
  const [view, setView] = useState<View>("chats");

  useEffect(() => {
    ensureSessionsWired();
    ensureCallsWired();
  }, []);

  const active = sessions.find((s) => s.id === activeId) ?? null;

  const viewSwitch = (
    <div className="flex items-center gap-1 rounded-md border p-1">
      <button
        onClick={() => setView("chats")}
        className={`flex items-center gap-1.5 rounded px-2.5 py-1 text-sm font-medium transition-colors ${
          view === "chats" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
        }`}
      >
        <MessageCircle className="h-4 w-4" />
        <span className="hidden sm:inline">Chats</span>
      </button>
      <button
        onClick={() => setView("calls")}
        className={`flex items-center gap-1.5 rounded px-2.5 py-1 text-sm font-medium transition-colors ${
          view === "calls" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
        }`}
      >
        <PhoneCall className="h-4 w-4" />
        <span className="hidden sm:inline">Calls</span>
      </button>
    </div>
  );

  return (
    <TooltipProvider delayDuration={200}>
      <AppShell viewSwitch={viewSwitch}>
        {sessions.length === 0 ? (
          <EmptyState
            icon={<PlusCircle className="h-6 w-6" />}
            title="No accounts yet"
            description="Create your first WhatsApp account from the sidebar to start chatting and calling."
          />
        ) : active ? (
          <div className="space-y-6">
            <SessionHeader session={active} />
            {active.paired ? (
              view === "chats" ? (
                <ChatsPage sid={active.id} />
              ) : (
                <CallsPage sid={active.id} />
              )
            ) : (
              <SessionPairing session={active} />
            )}
          </div>
        ) : (
          <EmptyState title="Select an account" description="Choose an account from the sidebar." />
        )}
      </AppShell>
      <IncomingCallModal />
      <Toaster theme={theme} position="top-right" richColors closeButton />
    </TooltipProvider>
  );
};
