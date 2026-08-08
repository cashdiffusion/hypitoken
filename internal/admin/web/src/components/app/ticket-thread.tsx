import { Loader2, Send, ShieldAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { Ticket, TicketStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

// STATUS_TONE maps a ticket status to its badge treatment. Open is the only one
// that reads as "needs attention" — amber rather than red, because an open
// ticket is a normal state of the world, not an error.
const STATUS_TONE: Record<TicketStatus, string> = {
  open: "border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400",
  pending: "border-sky-500/30 bg-sky-500/10 text-sky-600 dark:text-sky-400",
  resolved: "border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
  rejected: "border-muted-foreground/25 bg-muted/40 text-muted-foreground",
};

export function TicketStatusBadge({ status }: { status: TicketStatus }) {
  const { t } = useTranslation();
  return (
    <Badge variant="outline" className={cn("font-medium", STATUS_TONE[status])}>
      {t(`support.status.${status}`)}
    </Badge>
  );
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * TicketThread renders one conversation and its reply box. Shared verbatim by
 * the signed-in desk, the anonymous appeal page, and the operator queue — the
 * three differ only in who `onReply` posts as, so keeping one component means a
 * user and an operator are always looking at the same thread rendered the same
 * way.
 */
export function TicketThread({
  ticket,
  onReply,
  replyAs = "user",
  readOnly = false,
}: {
  ticket: Ticket;
  onReply?: (body: string) => Promise<void>;
  replyAs?: "user" | "admin";
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);

  const send = async () => {
    if (!onReply || !body.trim() || busy) return;
    setBusy(true);
    try {
      await onReply(body.trim());
      setBody("");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="space-y-3">
        {(ticket.messages ?? []).map((m) => {
          const fromMe = m.author === replyAs;
          return (
            <div key={m.id} className={cn("flex", fromMe ? "justify-end" : "justify-start")}>
              <div
                className={cn(
                  "max-w-[85%] rounded-2xl px-4 py-3 text-sm leading-relaxed whitespace-pre-wrap",
                  fromMe
                    ? "bg-primary/10 border border-primary/20"
                    : "bg-muted/60 border border-border/60",
                )}
              >
                <div className="mb-1 flex items-center gap-2 text-[11px] text-muted-foreground">
                  <span className="font-medium">
                    {m.author === "admin" ? t("support.thread.staff") : t("support.thread.you")}
                  </span>
                  <span>{fmtTime(m.created_at)}</span>
                </div>
                {m.body}
              </div>
            </div>
          );
        })}
      </div>

      {readOnly ? null : (
        <div className="space-y-2">
          <Textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder={t("support.thread.replyPlaceholder")}
            rows={3}
            className="resize-none"
          />
          <div className="flex justify-end">
            <Button size="sm" onClick={send} disabled={!body.trim() || busy}>
              {busy ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Send className="h-3.5 w-3.5" />
              )}
              {t("support.thread.send")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * AppealKeyNotice shows the access key returned when an appeal is filed without
 * a session. It is the submitter's only handle on the thread — they cannot log
 * in to find it again — so this deliberately reads as a warning rather than a
 * receipt, and the page stores it in localStorage as a safety net.
 */
export function AppealKeyNotice({ id, accessKey }: { id: number; accessKey: string }) {
  const { t } = useTranslation();
  return (
    <div className="rounded-xl border border-amber-500/30 bg-amber-500/5 p-4">
      <div className="mb-2 flex items-center gap-2 text-sm font-medium text-amber-600 dark:text-amber-400">
        <ShieldAlert className="h-4 w-4" />
        {t("support.appeal.keyTitle")}
      </div>
      <p className="mb-3 text-xs leading-relaxed text-muted-foreground">
        {t("support.appeal.keyHint")}
      </p>
      <code className="block rounded-lg bg-background/80 px-3 py-2 font-mono text-xs break-all">
        #{id} · {accessKey}
      </code>
    </div>
  );
}
