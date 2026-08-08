import { CheckCircle2, Loader2, ReceiptText, RefreshCw, TrendingUp, Wallet } from "lucide-react";
import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { InvoiceDialog } from "@/components/app/invoice-dialog";
import { CountUp, GlassPanel, PageHeader } from "@/components/app/page-primitives";
import { Pager } from "@/components/app/pager";
import { preloadStripe, StripeTopUp } from "@/components/app/stripe-topup";
import { SpotlightCard } from "@/components/landing/interactions";
import { Reveal } from "@/components/landing/reveal";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useAuth } from "@/hooks/use-auth";
import { apiGet, apiPost } from "@/lib/api";
import type { AlipayOrder, WalletTx } from "@/lib/types";
import { errMsg, fmtUSD } from "@/lib/utils";

const PRESETS = [5, 10, 20, 50, 100, 200];

// Server-side page size for both billing tables.
const PAGE = 50;
// Snapshot size for the lifetime-usage stat cards (matches the pre-pagination
// fetch depth so the cards keep their historical behavior).
const STATS_WINDOW = 200;

interface OrderStatus {
  status: "pending" | "paid" | "expired" | "failed";
  usd_credit: number;
  out_trade_no: string;
  publishable_key: string;
  client_secret: string;
}

export default function BillingPage() {
  const { t } = useTranslation();
  const { user, refresh } = useAuth();
  // Wallet-tx table page (server-side limit/offset pagination).
  const [tx, setTx] = useState<WalletTx[]>([]);
  const [txTotal, setTxTotal] = useState<number | undefined>(undefined);
  const [txOffset, setTxOffset] = useState(0);
  const [txBusy, setTxBusy] = useState(false);
  const [txLoaded, setTxLoaded] = useState(false);
  // Top-up orders table page.
  const [orders, setOrders] = useState<AlipayOrder[]>([]);
  const [ordersTotal, setOrdersTotal] = useState<number | undefined>(undefined);
  const [ordersOffset, setOrdersOffset] = useState(0);
  const [ordersBusy, setOrdersBusy] = useState(false);
  const [ordersLoaded, setOrdersLoaded] = useState(false);
  // Recent-tx snapshot used only by the lifetime/requests stat cards — kept
  // separate from the paginated table so paging doesn't change the cards.
  const [statsTx, setStatsTx] = useState<WalletTx[]>([]);
  const [open, setOpen] = useState(false);
  const [invoiceOpen, setInvoiceOpen] = useState(false);
  // Post-redirect settlement modal — Stripe Alipay/WeChat bounce back to
  // ?out=<order>; we poll it to paid and show the success animation.
  const [settleOut, setSettleOut] = useState<string | null>(null);
  const [settlePaid, setSettlePaid] = useState<number | null>(null);

  const loadTx = async (offset: number) => {
    setTxBusy(true);
    try {
      const r = await apiGet<{ transactions: WalletTx[]; total?: number }>(
        `/billing/transactions?limit=${PAGE}&offset=${offset}`,
      );
      setTx(r.transactions || []);
      setTxTotal(typeof r.total === "number" ? r.total : undefined);
      setTxOffset(offset);
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setTxBusy(false);
      setTxLoaded(true);
    }
  };

  const loadOrders = async (offset: number) => {
    setOrdersBusy(true);
    try {
      const r = await apiGet<{ orders: AlipayOrder[]; total?: number }>(
        `/billing/orders?limit=${PAGE}&offset=${offset}`,
      );
      setOrders(r.orders || []);
      setOrdersTotal(typeof r.total === "number" ? r.total : undefined);
      setOrdersOffset(offset);
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setOrdersBusy(false);
      setOrdersLoaded(true);
    }
  };

  const loadStats = async () => {
    try {
      // all=1 → full ledger incl. charges, which the lifetime/requests cards
      // tally (the wallet-history table fetches the charge-free default).
      const r = await apiGet<{ transactions: WalletTx[] }>(
        `/billing/transactions?limit=${STATS_WINDOW}&all=1`,
      );
      setStatsTx(r.transactions || []);
    } catch {
      // Stat cards are best-effort; the tables surface their own errors.
    }
  };

  // Full reload (refresh button / after a successful top-up): jump both tables
  // back to the first page so the newest rows are visible, and re-snapshot stats.
  const reload = async () => {
    await Promise.all([loadTx(0), loadOrders(0), loadStats()]);
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: mount-only fetch; reload is a stable inline async fn recreated each render but intentionally not re-run
  useEffect(() => {
    reload();
  }, []);

  // Post-redirect settle: Alipay / WeChat via Stripe Checkout bounce the browser
  // to a hosted auth page and back here with ?out=<order>. Show a settling modal,
  // poll the order to paid, then play the success animation. (Stripe doesn't
  // always append session_id to a custom-ui return_url, so trigger on ?out alone.)
  // biome-ignore lint/correctness/useExhaustiveDependencies: mount-only URL-param check; re-running on reload/refresh would re-trigger the settle flow on every re-render
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const out = params.get("out");
    if (!out) return;
    // Strip the return params so a refresh doesn't re-trigger.
    // Keep BrowserRouter's usr/key/idx state intact while removing the payment
    // return parameter, otherwise later SPA transitions can lose their index.
    window.history.replaceState(window.history.state, "", window.location.pathname);
    setSettleOut(out);
    let stop = false;
    (async () => {
      for (let i = 0; i < 90 && !stop; i++) {
        try {
          const r = await apiGet<OrderStatus>(`/billing/orders/${out}`);
          if (r.status === "paid") {
            setSettlePaid(r.usd_credit);
            await reload();
            await refresh();
            setTimeout(() => {
              if (!stop) {
                setSettleOut(null);
                setSettlePaid(null);
              }
            }, 2600);
            return;
          }
          if (r.status === "expired" || r.status === "failed") {
            toast.error(t("billing.stripe.failed"));
            setSettleOut(null);
            return;
          }
        } catch {}
        await new Promise((res) => setTimeout(res, 2000));
      }
      setSettleOut(null); // still pending after the poll window — close quietly
    })();
    return () => {
      stop = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const lifetime = statsTx
    .filter((w) => w.kind === "charge")
    .reduce((s, w) => s + Math.abs(w.amount_usd), 0);

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow={t("nav.billing")}
        title={t("billing.title")}
        sub={t("billing.sub")}
        action={
          <Button onClick={reload} variant="outline" size="sm" className="gap-2">
            <RefreshCw className="h-3.5 w-3.5" /> {t("common.refresh")}
          </Button>
        }
      />

      <Reveal>
        <div className="grid gap-4 md:grid-cols-2">
          <SpotlightCard className="ring-1 ring-primary/30">
            <div className="flex items-center justify-between">
              <span className="text-xs uppercase tracking-wider text-muted-foreground">
                {t("billing.currentBalance")}
              </span>
              <span className="grid h-8 w-8 place-items-center rounded-lg bg-primary/15 text-primary">
                <Wallet className="h-4 w-4" />
              </span>
            </div>
            <div className="mt-3 font-mono text-4xl font-semibold tracking-tight tabular-nums text-primary">
              <CountUp value={user?.balance_usd ?? 0} format={(n) => fmtUSD(n)} />
            </div>
            <div className="mt-4 flex flex-wrap items-center gap-3">
              <Button onClick={() => setOpen(true)} size="lg" className="gap-2">
                <Wallet className="h-4 w-4" /> {t("dashboard.topUp")}
              </Button>
            </div>
          </SpotlightCard>
          <SpotlightCard>
            <div className="flex items-center justify-between">
              <span className="text-xs uppercase tracking-wider text-muted-foreground">
                {t("billing.lifetimeUsage")}
              </span>
              <span className="grid h-8 w-8 place-items-center rounded-lg bg-muted/60 text-muted-foreground">
                <TrendingUp className="h-4 w-4" />
              </span>
            </div>
            <div className="mt-3 font-mono text-4xl font-semibold tracking-tight tabular-nums">
              <CountUp value={lifetime} format={(n) => fmtUSD(n)} />
            </div>
            <p className="mt-4 text-sm text-muted-foreground">
              {t("billing.requestsBilledTopups", {
                r: statsTx.filter((w) => w.kind === "charge").length,
                t: statsTx.filter((w) => w.kind === "topup").length,
              })}
            </p>
          </SpotlightCard>
        </div>
      </Reveal>

      <Reveal>
        <GlassPanel
          title={t("billing.topUpOrders")}
          description={t("billing.topUpOrdersSub")}
          bodyClassName="p-0"
        >
          {(() => {
            if (!ordersLoaded)
              return (
                <div className="flex items-center justify-center gap-2 p-8 text-sm text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" /> {t("common.loading")}
                </div>
              );
            // The server returns only active (pending|paid) orders, and its
            // `total` counts the same set — so rows and the pager agree.
            return (
              <>
                {orders.length === 0 ? (
                  <div className="p-8 text-center text-sm text-muted-foreground">
                    {t("billing.noActiveOrders")}
                  </div>
                ) : (
                  <div className={ordersBusy ? "pointer-events-none opacity-50" : undefined}>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{t("billing.columns.order")}</TableHead>
                          <TableHead className="text-right">{t("billing.columns.usd")}</TableHead>
                          <TableHead>{t("billing.columns.status")}</TableHead>
                          <TableHead>{t("billing.columns.created")}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {orders.map((o) => (
                          <TableRow key={o.out_trade_no}>
                            <TableCell className="font-mono text-xs">
                              {o.out_trade_no.slice(0, 16)}…
                            </TableCell>
                            <TableCell className="font-mono tabular-nums text-right">
                              {fmtUSD(o.usd_credit)}
                            </TableCell>
                            <TableCell>
                              <StatusPill status={o.status} />
                            </TableCell>
                            <TableCell className="text-muted-foreground">
                              {new Date(o.created_at * 1000).toLocaleString()}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                )}
                <Pager
                  offset={ordersOffset}
                  limit={PAGE}
                  total={ordersTotal}
                  count={orders.length}
                  busy={ordersBusy}
                  onChange={loadOrders}
                  className="border-t border-border px-4 py-3"
                />
              </>
            );
          })()}
        </GlassPanel>
      </Reveal>

      <Reveal>
        <GlassPanel
          title={t("billing.walletHistory")}
          description={
            <Trans
              i18nKey="billing.walletHistorySub"
              components={{
                logs: (
                  <Link
                    to="/app/logs"
                    className="underline underline-offset-2 text-foreground hover:text-primary"
                  />
                ),
              }}
            />
          }
          bodyClassName="p-0"
        >
          {(() => {
            if (!txLoaded)
              return (
                <div className="flex items-center justify-center gap-2 p-8 text-sm text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" /> {t("common.loading")}
                </div>
              );
            // Per-request charges are excluded server-side (they live in
            // /app/logs), so the rows and the pager `total` count the same
            // non-charge ledger.
            return (
              <>
                {tx.length === 0 ? (
                  <div className="p-8 text-center text-sm text-muted-foreground">
                    {t("billing.noWalletYet")}
                  </div>
                ) : (
                  <div className={txBusy ? "pointer-events-none opacity-50" : undefined}>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{t("billing.columns.kind")}</TableHead>
                          <TableHead className="text-right">
                            {t("billing.columns.amount")}
                          </TableHead>
                          <TableHead>{t("billing.columns.reference")}</TableHead>
                          <TableHead>{t("billing.columns.when")}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {tx.map((tr) => (
                          <TableRow key={tr.id}>
                            <TableCell className="capitalize font-medium">{tr.kind}</TableCell>
                            <TableCell
                              className={`font-mono tabular-nums text-right ${tr.amount_usd >= 0 ? "text-success" : ""}`}
                            >
                              {tr.amount_usd >= 0 ? "+" : ""}
                              {fmtUSD(tr.amount_usd)}
                            </TableCell>
                            <TableCell className="font-mono text-xs text-muted-foreground">
                              {tr.ref || "—"}
                            </TableCell>
                            <TableCell className="text-muted-foreground">
                              {new Date(tr.created_at * 1000).toLocaleString()}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                )}
                <Pager
                  offset={txOffset}
                  limit={PAGE}
                  total={txTotal}
                  count={tx.length}
                  busy={txBusy}
                  onChange={loadTx}
                  className="border-t border-border px-4 py-3"
                />
              </>
            );
          })()}
        </GlassPanel>
      </Reveal>

      <TopUpDialog
        open={open}
        onOpenChange={setOpen}
        onPaid={async () => {
          await reload();
          await refresh();
        }}
        onInvoice={() => setInvoiceOpen(true)}
      />

      <InvoiceDialog open={invoiceOpen} onOpenChange={setInvoiceOpen} />

      {/* Post-redirect settlement: confirming spinner → paid animation. */}
      <Dialog
        open={settleOut !== null}
        onOpenChange={(v) => {
          if (!v) {
            setSettleOut(null);
            setSettlePaid(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-[420px]">
          {settlePaid !== null ? (
            <PaidState usd={settlePaid} />
          ) : (
            <div className="flex flex-col items-center gap-3 py-10 text-center">
              <Loader2 className="h-8 w-8 animate-spin text-primary" />
              <p className="text-sm text-muted-foreground">{t("billing.stripe.confirming")}</p>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const { t } = useTranslation();
  const cls =
    status === "paid"
      ? "bg-success/15 text-success border-success/30"
      : status === "pending"
        ? "bg-warning/15 text-warning border-warning/30"
        : "bg-destructive/15 text-destructive border-destructive/30";
  const label =
    status === "paid"
      ? t("common.paid")
      : status === "pending"
        ? t("common.pending")
        : status === "expired"
          ? t("common.expired")
          : status === "failed"
            ? t("common.failed")
            : status;
  return (
    <span
      className={`inline-flex rounded border px-2 py-0.5 text-xs font-mono uppercase tracking-wider ${cls}`}
    >
      {label}
    </span>
  );
}

type ProvidersInfo = {
  stripe: { enabled: boolean; publishable_key?: string; currency?: string };
  qr: { enabled: boolean };
};

function TopUpDialog({
  open,
  onOpenChange,
  onPaid,
  onInvoice,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onPaid: () => void;
  /** Close this dialog and open the invoice-request flow instead. */
  onInvoice: () => void;
}) {
  const { t } = useTranslation();
  const [usd, setUsd] = useState("10");
  const [order, setOrder] = useState<OrderStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [polling, setPolling] = useState(false);
  const [paid, setPaid] = useState(false);

  // Stripe is the only top-up rail (USD wallet; Adaptive Pricing localizes the
  // buyer's currency at checkout so Alipay et al. work). The legacy QR/Z-Pay
  // rail is no longer surfaced in the UI.
  const [providers, setProviders] = useState<ProvidersInfo | null>(null);

  useEffect(() => {
    if (!open) return;
    apiGet<ProvidersInfo>("/billing/providers")
      .then((p) => {
        setProviders(p);
        // Warm Stripe.js now (dialog just opened, user is still choosing an
        // amount) so the Checkout Element appears fast once they hit "下单".
        preloadStripe(p.stripe?.publishable_key);
      })
      .catch(() => setProviders({ stripe: { enabled: false }, qr: { enabled: false } }));
  }, [open]);

  const amount = parseFloat(usd || "0");
  const stripeEnabled = !!providers?.stripe?.enabled;

  const create = async () => {
    setBusy(true);
    try {
      const r = await apiPost<OrderStatus>("/billing/topup", { usd: amount, provider: "stripe" });
      setOrder(r);
      // Wait for the Checkout Element to confirm before polling.
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  const pollOrder = async (out: string) => {
    setPolling(true);
    for (let i = 0; i < 90; i++) {
      await new Promise((r) => setTimeout(r, 2000));
      try {
        const r = await apiGet<OrderStatus>(`/billing/orders/${out}`);
        if (r.status === "paid") {
          setPaid(true);
          setPolling(false);
          toast.success(t("billing.dialog.paid", { n: r.usd_credit }));
          onPaid();
          setTimeout(() => close(), 1400);
          return;
        }
        if (r.status === "expired" || r.status === "failed") {
          setPolling(false);
          toast.error(t("billing.stripe.failed"));
          return;
        }
      } catch {}
    }
    setPolling(false);
  };

  const close = () => {
    onOpenChange(false);
    setOrder(null);
    setPolling(false);
    setPaid(false);
    setUsd("10");
  };

  return (
    <Dialog open={open} onOpenChange={(v) => (!v ? close() : onOpenChange(true))}>
      <DialogContent className="sm:max-w-[520px]">
        {!order ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("billing.dialog.title")}</DialogTitle>
              <DialogDescription>{t("billing.dialog.sub")}</DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-2">
              <div className="space-y-2">
                <Label htmlFor="usd">{t("billing.dialog.amountLabel")}</Label>
                <Input
                  id="usd"
                  type="number"
                  min="1"
                  max="1000"
                  step="1"
                  value={usd}
                  onChange={(e) => setUsd(e.target.value)}
                  className="font-mono text-2xl"
                />
              </div>
              <div className="grid grid-cols-3 gap-2">
                {PRESETS.map((p) => (
                  <Button
                    key={p}
                    type="button"
                    variant={amount === p ? "default" : "outline"}
                    size="sm"
                    onClick={() => setUsd(String(p))}
                  >
                    ${p}
                  </Button>
                ))}
              </div>

              <div className="space-y-3">
                <MethodChips />
                <div className="rounded-md border border-border-strong bg-muted/30 p-3 text-sm">
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">{t("billing.stripe.youPay")}</span>
                    <span className="font-mono tabular-nums">{fmtUSD(amount)}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">
                      {t("billing.dialog.walletCredit")}
                    </span>
                    <span className="font-mono tabular-nums">{fmtUSD(amount)}</span>
                  </div>
                  <div className="mt-1 text-[11px] text-muted-foreground">
                    {t("billing.stripe.localizeNote")}
                  </div>
                </div>
                {providers && !stripeEnabled && (
                  <p className="text-sm text-destructive">{t("billing.stripe.unavailable")}</p>
                )}
                {/* Anyone who needs a 发票 has to pay by 对公转账 instead — say so
                    here, before they pay by card and find out afterwards. */}
                <div className="rounded-md border border-primary/20 bg-primary/5 p-3 text-xs leading-relaxed">
                  <div className="mb-1 flex items-center gap-1.5 font-medium">
                    <ReceiptText className="h-3.5 w-3.5 text-primary" />
                    {t("billing.invoiceNotice.title")}
                  </div>
                  <p className="text-muted-foreground">{t("billing.invoiceNotice.body")}</p>
                  <Button
                    variant="link"
                    size="sm"
                    className="h-auto px-0 py-1 text-xs"
                    onClick={() => {
                      close();
                      onInvoice();
                    }}
                  >
                    {t("billing.invoiceNotice.action")}
                  </Button>
                </div>
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={close}>
                {t("billing.dialog.cancel")}
              </Button>
              <Button onClick={create} disabled={busy || !(amount >= 1) || !stripeEnabled}>
                {busy ? t("billing.dialog.creating") : t("billing.dialog.create")}
              </Button>
            </DialogFooter>
          </>
        ) : paid ? (
          <PaidState usd={order.usd_credit} />
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t("billing.stripe.payTitle")}</DialogTitle>
              <DialogDescription>{t("billing.stripe.payDesc")}</DialogDescription>
            </DialogHeader>
            <div className="py-2">
              {polling ? (
                <div className="flex flex-col items-center gap-3 py-8 text-center">
                  <Loader2 className="h-8 w-8 animate-spin text-primary" />
                  <p className="text-sm text-muted-foreground">{t("billing.stripe.confirming")}</p>
                  <code className="text-xs text-muted-foreground">{order.out_trade_no}</code>
                </div>
              ) : (
                <StripeTopUp
                  publishableKey={order.publishable_key}
                  clientSecret={order.client_secret}
                  onConfirmed={() => pollOrder(order.out_trade_no)}
                />
              )}
            </div>
            {!polling && (
              <DialogFooter>
                <Button variant="outline" onClick={close}>
                  {t("billing.dialog.cancel")}
                </Button>
              </DialogFooter>
            )}
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

// MethodChips previews the rails the Stripe Payment Element will surface. Only
// Card + Alipay are enabled on the account; the Element filters by eligibility
// anyway, so we don't advertise rails (WeChat / crypto) that won't appear.
function MethodChips() {
  const { t } = useTranslation();
  const methods = [
    { label: t("billing.stripe.methods.card"), cls: "text-foreground" },
    { label: "Alipay 支付宝", cls: "text-[#1677FF]" },
  ];
  return (
    <div className="flex flex-wrap gap-1.5">
      {methods.map((m) => (
        <span
          key={m.label}
          className={`rounded-md border border-border-strong bg-muted/30 px-2 py-1 text-[11px] font-medium ${m.cls}`}
        >
          {m.label}
        </span>
      ))}
    </div>
  );
}

function PaidState({ usd }: { usd: number }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <CheckCircle2 className="h-12 w-12 text-success" />
      <div className="text-lg font-semibold">{t("billing.stripe.success")}</div>
      <div className="font-mono text-2xl font-semibold tabular-nums text-primary">
        +{fmtUSD(usd)}
      </div>
    </div>
  );
}
