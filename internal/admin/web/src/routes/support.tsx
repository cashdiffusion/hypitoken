import { ChevronLeft, LifeBuoy, MessageSquarePlus, Plus } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { GlassPanel, PageHeader } from "@/components/app/page-primitives";
import { Pager } from "@/components/app/pager";
import { TicketStatusBadge, TicketThread } from "@/components/app/ticket-thread";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { apiGet, apiPost } from "@/lib/api";
import type { Ticket, TicketList } from "@/lib/types";
import { errMsg } from "@/lib/utils";

const PAGE = 20;

export default function SupportPage() {
  const { t } = useTranslation();
  const [list, setList] = useState<Ticket[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [active, setActive] = useState<Ticket | null>(null);
  const [composing, setComposing] = useState(false);
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const r = await apiGet<TicketList>(`/me/tickets?limit=${PAGE}&offset=${offset}`);
      setList(r.tickets ?? []);
      setTotal(r.total ?? 0);
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.load")));
    }
  }, [offset, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const open = async (id: number) => {
    try {
      const r = await apiGet<{ ticket: Ticket }>(`/me/tickets/${id}`);
      setActive(r.ticket);
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.load")));
    }
  };

  const create = async () => {
    if (!body.trim() || busy) return;
    setBusy(true);
    try {
      const r = await apiPost<{ ticket: Ticket }>("/me/tickets", {
        subject: subject.trim(),
        body: body.trim(),
      });
      toast.success(t("support.created"));
      setComposing(false);
      setSubject("");
      setBody("");
      setActive(r.ticket);
      void load();
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.create")));
    } finally {
      setBusy(false);
    }
  };

  const reply = async (text: string) => {
    if (!active) return;
    try {
      const r = await apiPost<{ ticket: Ticket }>(`/me/tickets/${active.id}/reply`, { body: text });
      setActive(r.ticket);
      void load();
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.reply")));
    }
  };

  // ---- single thread ----
  if (active) {
    return (
      <div className="space-y-6">
        <Button variant="ghost" size="sm" onClick={() => setActive(null)} className="-ml-2">
          <ChevronLeft className="h-4 w-4" />
          {t("support.backToList")}
        </Button>
        <GlassPanel className="p-6">
          <div className="mb-5 flex flex-wrap items-center justify-between gap-3 border-b border-border/50 pb-4">
            <div>
              <div className="text-base font-medium">{active.subject}</div>
              <div className="text-xs text-muted-foreground">#{active.id}</div>
            </div>
            <TicketStatusBadge status={active.status} />
          </div>
          <TicketThread ticket={active} onReply={reply} replyAs="user" />
        </GlassPanel>
      </div>
    );
  }

  // ---- list + composer ----
  return (
    <div className="space-y-6">
      <PageHeader
        icon={LifeBuoy}
        title={t("support.title")}
        sub={t("support.sub")}
        action={
          composing ? null : (
            <Button size="sm" onClick={() => setComposing(true)}>
              <Plus className="h-4 w-4" />
              {t("support.newTicket")}
            </Button>
          )
        }
      />

      {composing ? (
        <GlassPanel className="space-y-4 p-6">
          <div className="space-y-2">
            <Label htmlFor="subject">{t("support.form.subject")}</Label>
            <Input
              id="subject"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder={t("support.form.subjectPlaceholder")}
              maxLength={200}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="body">{t("support.form.body")}</Label>
            <Textarea
              id="body"
              value={body}
              onChange={(e) => setBody(e.target.value)}
              placeholder={t("support.form.bodyPlaceholder")}
              rows={6}
              className="resize-none"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => setComposing(false)}>
              {t("common.cancel")}
            </Button>
            <Button size="sm" onClick={create} disabled={!body.trim() || busy}>
              <MessageSquarePlus className="h-4 w-4" />
              {t("support.form.submit")}
            </Button>
          </div>
        </GlassPanel>
      ) : null}

      {list.length === 0 && !composing ? (
        <GlassPanel className="flex flex-col items-center gap-3 p-12 text-center">
          <LifeBuoy className="h-8 w-8 text-muted-foreground/50" />
          <div className="text-sm text-muted-foreground">{t("support.empty")}</div>
        </GlassPanel>
      ) : (
        <div className="space-y-2">
          {list.map((tk) => (
            <button
              key={tk.id}
              type="button"
              onClick={() => open(tk.id)}
              className="flex w-full items-center justify-between gap-4 rounded-xl border border-border/60 bg-card/40 px-4 py-3 text-left transition hover:border-primary/30 hover:bg-card/70"
            >
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{tk.subject}</div>
                <div className="text-xs text-muted-foreground">
                  #{tk.id} · {new Date(tk.updated_at).toLocaleString()}
                </div>
              </div>
              <TicketStatusBadge status={tk.status} />
            </button>
          ))}
          <Pager
            total={total}
            limit={PAGE}
            offset={offset}
            count={list.length}
            onChange={setOffset}
          />
        </div>
      )}
    </div>
  );
}
