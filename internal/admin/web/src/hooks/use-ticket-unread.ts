import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { useAuth } from "@/hooks/use-auth";
import { apiGet } from "@/lib/api";

// Server contract: unread = tickets where the admin replied after the user
// last opened them; latest = the most recently updated such ticket.
interface UnreadResp {
  unread: number;
  latest: { id: number; subject: string; updated_at: number } | null;
}

// Fired by the support page after a ticket detail fetch resolves — the server
// marks the ticket seen on that GET, so listeners re-pull the unread count.
export const TICKETS_SEEN_EVENT = "hypi:tickets-seen";

// Remembers the last (id, updated_at) we toasted about, so a remount or page
// reload doesn't nag — only a strictly newer admin reply prompts again.
const PROMPT_KEY = "hypi.tickets.prompted";

const POLL_MS = 60_000;

// useTicketUnread polls the unread-admin-replies count while signed in.
// `prompt: true` (exactly one mount — the AppShell) additionally raises a
// one-shot sonner toast when a not-yet-announced reply is waiting. All fetch
// failures are silent: a broken badge must never surface an error.
export function useTicketUnread({ prompt = false }: { prompt?: boolean } = {}) {
  const { user } = useAuth();
  const { pathname } = useLocation();
  const nav = useNavigate();
  const { t } = useTranslation();
  const [unread, setUnread] = useState(0);
  const latestRef = useRef<UnreadResp["latest"]>(null);
  // Sticky across refreshes within a mount so the poll can't re-toast.
  const promptRef = useRef(prompt);
  promptRef.current = prompt;

  const refresh = useCallback(async () => {
    try {
      const r = await apiGet<UnreadResp>("/me/tickets/unread");
      setUnread(r.unread ?? 0);
      latestRef.current = r.latest ?? null;
      const l = r.latest;
      if (!promptRef.current || !r.unread || !l) return;
      const stamp = `${l.id}:${l.updated_at}`;
      if (localStorage.getItem(PROMPT_KEY) === stamp) return;
      localStorage.setItem(PROMPT_KEY, stamp);
      toast(t("support.unreadPrompt.title"), {
        description: t("support.unreadPrompt.body", { subject: l.subject }),
        duration: 12_000,
        action: {
          label: t("support.unreadPrompt.action"),
          onClick: () => nav(`/app/support?open=${l.id}`),
        },
      });
    } catch {
      // Silent by design: the badge simply keeps its last value.
    }
  }, [t, nav]);

  const authed = !!user;
  const onSupport = pathname.startsWith("/app/support");

  // biome-ignore lint/correctness/useExhaustiveDependencies: onSupport deliberately re-arms the effect so entering the tickets page triggers an immediate refresh
  useEffect(() => {
    if (!authed) {
      setUnread(0);
      return;
    }
    void refresh();
    const id = window.setInterval(() => void refresh(), POLL_MS);
    const onSeen = () => void refresh();
    window.addEventListener(TICKETS_SEEN_EVENT, onSeen);
    return () => {
      window.clearInterval(id);
      window.removeEventListener(TICKETS_SEEN_EVENT, onSeen);
    };
  }, [authed, onSupport, refresh]);

  return { unread, refresh };
}
