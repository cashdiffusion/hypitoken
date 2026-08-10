import {
  CheckCircle2,
  ChevronLeft,
  Copy,
  Inbox,
  ReceiptText,
  ShieldQuestion,
  XCircle,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Pager } from "@/components/app/pager";
import { TicketStatusBadge, TicketThread } from "@/components/app/ticket-thread";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { apiGet, apiPost } from "@/lib/api";
import type { Ticket, TicketList, TicketStatus } from "@/lib/types";
import { cn, errMsg } from "@/lib/utils";

const PAGE = 20;

const FILTERS: { value: string; labelKey: string }[] = [
  { value: "live", labelKey: "support.admin.filters.live" },
  { value: "open", labelKey: "support.admin.filters.open" },
  { value: "pending", labelKey: "support.admin.filters.pending" },
  { value: "resolved", labelKey: "support.admin.filters.resolved" },
  { value: "rejected", labelKey: "support.admin.filters.rejected" },
  { value: "", labelKey: "support.admin.filters.all" },
];

type InvoiceMeta = {
  kind?: string;
  title?: { name?: string; tax_no?: string; address?: string; phone?: string };
  amount_cny?: number;
  note?: string;
};

/** InvoiceMetaPanel renders a 开票申请's structured 抬头. A transposed digit in an
 *  18-character tax number produces an invoice the customer's finance team will
 *  reject, so every value is copyable rather than retyped by eye. */
function InvoiceMetaPanel({ raw }: { raw: string }) {
  const { t } = useTranslation();
  let meta: InvoiceMeta | null = null;
  try {
    meta = JSON.parse(raw) as InvoiceMeta;
  } catch {
    return null;
  }
  if (!meta?.title?.name) return null;
  const rows: [string, string][] = [
    [t("invoice.form.company"), meta.title.name],
    [t("invoice.form.taxNo"), meta.title.tax_no ?? ""],
  ];
  if (meta.title.address) rows.push([t("invoice.admin.address"), meta.title.address]);
  if (meta.title.phone) rows.push([t("invoice.admin.phone"), meta.title.phone]);
  if (meta.amount_cny) rows.push([t("invoice.form.amount"), `￥${meta.amount_cny.toFixed(2)}`]);
  if (meta.note) rows.push([t("invoice.form.note"), meta.note]);

  return (
    <div className="mb-5 rounded-xl border border-primary/20 bg-primary/5 p-4">
      <div className="mb-2 flex items-center gap-1.5 text-sm font-medium">
        <ReceiptText className="h-4 w-4 text-primary" />
        {t("invoice.admin.title")}
      </div>
      <div className="space-y-1">
        {rows.map(([label, value]) => (
          <div key={label} className="flex items-start justify-between gap-3 text-sm">
            <span className="shrink-0 text-xs text-muted-foreground">{label}</span>
            <div className="flex min-w-0 items-start gap-2">
              <span className="text-right font-mono break-all">{value}</span>
              <button
                type="button"
                onClick={() => navigator.clipboard?.writeText(value)}
                className="mt-0.5 shrink-0 text-muted-foreground transition hover:text-foreground"
                aria-label={label}
              >
                <Copy className="h-3 w-3" />
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

export function TicketsTab() {
  const { t } = useTranslation();
  const [filter, setFilter] = useState("live");
  const [list, setList] = useState<Ticket[]>([]);
  const [total, setTotal] = useState(0);
  const [openCount, setOpenCount] = useState(0);
  const [offset, setOffset] = useState(0);
  const [active, setActive] = useState<Ticket | null>(null);

  const load = useCallback(async () => {
    try {
      const r = await apiGet<TicketList>(
        `/admin/tickets?status=${filter}&limit=${PAGE}&offset=${offset}`,
      );
      setList(r.tickets ?? []);
      setTotal(r.total ?? 0);
      setOpenCount(r.open ?? 0);
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.load")));
    }
  }, [filter, offset, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const open = async (id: number) => {
    try {
      const r = await apiGet<{ ticket: Ticket }>(`/admin/tickets/${id}`);
      setActive(r.ticket);
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.load")));
    }
  };

  const reply = async (body: string) => {
    if (!active) return;
    try {
      const r = await apiPost<{ ticket: Ticket }>(`/admin/tickets/${active.id}/reply`, { body });
      setActive(r.ticket);
      void load();
      toast.success(t("support.admin.replied"));
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.reply")));
    }
  };

  const setStatus = async (status: TicketStatus) => {
    if (!active) return;
    try {
      const r = await apiPost<{ ticket: Ticket }>(`/admin/tickets/${active.id}/status`, { status });
      setActive(r.ticket);
      void load();
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.status")));
    }
  };

  if (active) {
    return (
      <div className="space-y-5">
        <Button variant="ghost" size="sm" onClick={() => setActive(null)} className="-ml-2">
          <ChevronLeft className="h-4 w-4" />
          {t("support.backToList")}
        </Button>

        <div className="rounded-2xl border border-border/60 bg-card/40 p-6">
          <div className="mb-5 flex flex-wrap items-center justify-between gap-3 border-b border-border/50 pb-4">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                {active.kind === "appeal" ? (
                  <ShieldQuestion className="h-4 w-4 shrink-0 text-amber-500" />
                ) : null}
                {active.kind === "invoice" ? (
                  <ReceiptText className="h-4 w-4 shrink-0 text-primary" />
                ) : null}
                <span className="truncate text-base font-medium">{active.subject}</span>
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">
                #{active.id} · {active.email}
                {active.user_id > 0
                  ? ` · user ${active.user_id}`
                  : ` · ${t("support.admin.noAccount")}`}
              </div>
            </div>
            <div className="flex items-center gap-2">
              <TicketStatusBadge status={active.status} />
              <Button size="sm" variant="outline" onClick={() => setStatus("resolved")}>
                <CheckCircle2 className="h-3.5 w-3.5" />
                {t("support.admin.resolve")}
              </Button>
              <Button size="sm" variant="outline" onClick={() => setStatus("rejected")}>
                <XCircle className="h-3.5 w-3.5" />
                {t("support.admin.reject")}
              </Button>
            </div>
          </div>
          {active.kind === "invoice" && active.meta ? <InvoiceMetaPanel raw={active.meta} /> : null}
          <TicketThread ticket={active} onReply={reply} replyAs="admin" />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        {FILTERS.map((f) => (
          <button
            key={f.value || "all"}
            type="button"
            onClick={() => {
              setFilter(f.value);
              setOffset(0);
            }}
            className={cn(
              "rounded-full border px-3 py-1 text-xs transition",
              filter === f.value
                ? "border-primary/40 bg-primary/10 text-primary"
                : "border-border/60 text-muted-foreground hover:border-primary/25",
            )}
          >
            {t(f.labelKey)}
          </button>
        ))}
        {openCount > 0 ? (
          <Badge
            variant="outline"
            className="ml-auto border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400"
          >
            {t("support.admin.awaiting", { count: openCount })}
          </Badge>
        ) : null}
      </div>

      {list.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-2xl border border-border/60 bg-card/40 p-12 text-center">
          <Inbox className="h-8 w-8 text-muted-foreground/50" />
          <div className="text-sm text-muted-foreground">{t("support.admin.empty")}</div>
        </div>
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
                <div className="flex items-center gap-2">
                  {tk.kind === "appeal" ? (
                    <ShieldQuestion className="h-3.5 w-3.5 shrink-0 text-amber-500" />
                  ) : null}
                  {tk.kind === "invoice" ? (
                    <ReceiptText className="h-3.5 w-3.5 shrink-0 text-primary" />
                  ) : null}
                  <span className="truncate text-sm font-medium">{tk.subject}</span>
                </div>
                <div className="mt-0.5 truncate text-xs text-muted-foreground">
                  #{tk.id} · {tk.email} · {new Date(tk.updated_at).toLocaleString()}
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
