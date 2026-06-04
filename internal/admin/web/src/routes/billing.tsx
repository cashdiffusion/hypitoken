import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { useTranslation, Trans } from "react-i18next";
import { QRCodeSVG } from "qrcode.react";
import { Wallet, RefreshCw, Lock, TrendingUp, CreditCard, QrCode, CheckCircle2, Loader2 } from "lucide-react";
import { StripeTopUp } from "@/components/app/stripe-topup";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Reveal } from "@/components/landing/reveal";
import { SpotlightCard } from "@/components/landing/interactions";
import { PageHeader, CountUp, GlassPanel } from "@/components/app/page-primitives";
import { apiGet, apiPost } from "@/lib/api";
import { useAuth } from "@/hooks/use-auth";
import { fmtUSD } from "@/lib/utils";
import type { AlipayOrder, ExchangeRate, WalletTx } from "@/lib/types";

const PRESETS = [5, 10, 20, 50, 100, 200];

export default function BillingPage() {
  const { t } = useTranslation();
  const { user, refresh } = useAuth();
  const [tx, setTx] = useState<WalletTx[]>([]);
  const [orders, setOrders] = useState<AlipayOrder[]>([]);
  const [rate, setRate] = useState<ExchangeRate | null>(null);
  const [open, setOpen] = useState(false);

  const reload = async () => {
    const [t, o, r] = await Promise.all([
      apiGet<{ transactions: WalletTx[] }>("/billing/transactions"),
      apiGet<{ orders: AlipayOrder[] }>("/billing/orders"),
      apiGet<ExchangeRate>("/billing/rate"),
    ]);
    setTx(t.transactions || []);
    setOrders(o.orders || []);
    setRate(r);
  };

  useEffect(() => {
    reload();
  }, []);

  // Post-redirect settle: Alipay / WeChat / crypto via Stripe bounce the
  // browser to a hosted auth page and back here with ?out=<order>&
  // redirect_status=<...>. Resume polling that order, then clean the URL.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const out = params.get("out");
    if (!out || !params.get("redirect_status")) return;
    let stop = false;
    (async () => {
      for (let i = 0; i < 90 && !stop; i++) {
        try {
          const r = await apiGet<any>(`/billing/orders/${out}`);
          if (r.status === "paid") {
            toast.success(t("billing.dialog.paid", { n: r.usd_credit }));
            await reload();
            await refresh();
            break;
          }
          if (r.status === "expired" || r.status === "failed") {
            toast.error(t("billing.stripe.failed"));
            break;
          }
        } catch {}
        await new Promise((res) => setTimeout(res, 2000));
      }
    })();
    // Strip the Stripe return params so a refresh doesn't re-trigger.
    window.history.replaceState({}, "", window.location.pathname);
    return () => { stop = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const lifetime = tx.filter(t => t.kind === "charge").reduce((s, t) => s + Math.abs(t.amount_usd), 0);

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
              <span className="text-xs uppercase tracking-wider text-muted-foreground">{t("billing.currentBalance")}</span>
              <span className="grid h-8 w-8 place-items-center rounded-lg bg-primary/15 text-primary"><Wallet className="h-4 w-4" /></span>
            </div>
            <div className="mt-3 font-mono text-4xl font-semibold tracking-tight tabular-nums text-primary">
              <CountUp value={user?.balance_usd ?? 0} format={(n) => fmtUSD(n)} />
            </div>
            <div className="mt-4 flex flex-wrap items-center gap-3">
              <Button onClick={() => setOpen(true)} size="lg" className="gap-2"><Wallet className="h-4 w-4" /> {t("dashboard.topUp")}</Button>
              {rate && <p className="text-xs text-muted-foreground">{t("billing.liveRate", { rate: rate.cny_per_usd.toFixed(4) })}</p>}
            </div>
          </SpotlightCard>
          <SpotlightCard>
            <div className="flex items-center justify-between">
              <span className="text-xs uppercase tracking-wider text-muted-foreground">{t("billing.lifetimeUsage")}</span>
              <span className="grid h-8 w-8 place-items-center rounded-lg bg-muted/60 text-muted-foreground"><TrendingUp className="h-4 w-4" /></span>
            </div>
            <div className="mt-3 font-mono text-4xl font-semibold tracking-tight tabular-nums">
              <CountUp value={lifetime} format={(n) => fmtUSD(n)} />
            </div>
            <p className="mt-4 text-sm text-muted-foreground">{t("billing.requestsBilledTopups", { r: tx.filter(t => t.kind === "charge").length, t: tx.filter(t => t.kind === "topup").length })}</p>
          </SpotlightCard>
        </div>
      </Reveal>

      <Reveal>
        <GlassPanel title={t("billing.topUpOrders")} description={t("billing.topUpOrdersSub")} bodyClassName="p-0">
          {(() => {
            const visible = orders.filter((o) => o.status === "pending" || o.status === "paid");
            if (visible.length === 0) return <div className="p-8 text-center text-sm text-muted-foreground">{t("billing.noActiveOrders")}</div>;
            return (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("billing.columns.order")}</TableHead>
                  <TableHead className="text-right">{t("billing.columns.usd")}</TableHead>
                  <TableHead className="text-right">{t("billing.columns.cny")}</TableHead>
                  <TableHead className="text-right">{t("billing.columns.rate")}</TableHead>
                  <TableHead>{t("billing.columns.status")}</TableHead>
                  <TableHead>{t("billing.columns.created")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((o) => (
                  <TableRow key={o.out_trade_no}>
                    <TableCell className="font-mono text-xs">{o.out_trade_no.slice(0, 16)}…</TableCell>
                    <TableCell className="font-mono tabular-nums text-right">{fmtUSD(o.usd_credit)}</TableCell>
                    <TableCell className="font-mono tabular-nums text-right">¥{o.cny_amount.toFixed(2)}</TableCell>
                    <TableCell className="font-mono tabular-nums text-right text-muted-foreground">{o.rate.toFixed(4)}</TableCell>
                    <TableCell><StatusPill status={o.status} /></TableCell>
                    <TableCell className="text-muted-foreground">{new Date(o.created_at * 1000).toLocaleString()}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
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
              components={{ logs: <Link to="/app/logs" className="underline underline-offset-2 text-foreground hover:text-primary" /> }}
            />
          }
          bodyClassName="p-0"
        >
          {(() => {
            const visible = tx.filter((t) => t.kind !== "charge");
            if (visible.length === 0) return <div className="p-8 text-center text-sm text-muted-foreground">{t("billing.noWalletYet")}</div>;
            return (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("billing.columns.kind")}</TableHead>
                  <TableHead className="text-right">{t("billing.columns.amount")}</TableHead>
                  <TableHead>{t("billing.columns.reference")}</TableHead>
                  <TableHead>{t("billing.columns.when")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.slice(0, 50).map((tr) => (
                  <TableRow key={tr.id}>
                    <TableCell className="capitalize font-medium">{tr.kind}</TableCell>
                    <TableCell className={`font-mono tabular-nums text-right ${tr.amount_usd >= 0 ? "text-success" : ""}`}>{tr.amount_usd >= 0 ? "+" : ""}{fmtUSD(tr.amount_usd)}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">{tr.ref || "—"}</TableCell>
                    <TableCell className="text-muted-foreground">{new Date(tr.created_at * 1000).toLocaleString()}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            );
          })()}
        </GlassPanel>
      </Reveal>

      <TopUpDialog open={open} onOpenChange={setOpen} rate={rate} onPaid={async () => { await reload(); await refresh(); }} />
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const { t } = useTranslation();
  const cls =
    status === "paid" ? "bg-success/15 text-success border-success/30"
    : status === "pending" ? "bg-warning/15 text-warning border-warning/30"
    : "bg-destructive/15 text-destructive border-destructive/30";
  const label = status === "paid" ? t("common.paid")
    : status === "pending" ? t("common.pending")
    : status === "expired" ? t("common.expired")
    : status === "failed" ? t("common.failed") : status;
  return <span className={`inline-flex rounded border px-2 py-0.5 text-xs font-mono uppercase tracking-wider ${cls}`}>{label}</span>;
}

type ProvidersInfo = {
  stripe: { enabled: boolean; publishable_key?: string; currency?: string };
  qr: { enabled: boolean };
};

function TopUpDialog({ open, onOpenChange, rate: initialRate, onPaid }: any) {
  const { t } = useTranslation();
  const [usd, setUsd] = useState("10");
  const [order, setOrder] = useState<any>(null);
  const [busy, setBusy] = useState(false);
  const [polling, setPolling] = useState(false);
  const [paid, setPaid] = useState(false);

  // Which rails are available + which one is selected. Stripe is preferred
  // when enabled; the QR rail (Z-Pay / Alipay) stays as a fallback.
  const [providers, setProviders] = useState<ProvidersInfo | null>(null);
  const [provider, setProvider] = useState<"stripe" | "qr">("stripe");

  // Live CNY rate (only relevant for the QR rail; Stripe charges 1:1 in USD).
  const [liveRate, setLiveRate] = useState<ExchangeRate | null>(initialRate);
  const [rateAge, setRateAge] = useState(0);

  useEffect(() => {
    if (!open) return;
    apiGet<ProvidersInfo>("/billing/providers")
      .then((p) => {
        setProviders(p);
        setProvider(p.stripe?.enabled ? "stripe" : "qr");
      })
      .catch(() => setProviders({ stripe: { enabled: false }, qr: { enabled: true } }));
  }, [open]);

  useEffect(() => {
    // Need a live CNY rate for the QR rail and for Stripe-in-CNY.
    const needRate = provider === "qr" || providers?.stripe?.currency === "cny";
    if (!open || order || !needRate) return;
    let cancelled = false;
    const tick = async () => {
      try {
        const r = await apiGet<ExchangeRate>("/billing/rate");
        if (!cancelled) { setLiveRate(r); setRateAge(0); }
      } catch {}
    };
    tick();
    const fetchT = setInterval(tick, 30_000);
    const ageT = setInterval(() => setRateAge((a) => a + 1), 1000);
    return () => { cancelled = true; clearInterval(fetchT); clearInterval(ageT); };
  }, [open, order, provider, providers]);
  const rate = liveRate;

  const amount = parseFloat(usd || "0");
  const stripeEnabled = !!providers?.stripe?.enabled;
  const qrEnabled = !!providers?.qr?.enabled;
  // Stripe presentment currency. "cny" means charge in CNY via the live rate
  // (so Alipay works on a non-US account); else USD 1:1.
  const stripeCNY = providers?.stripe?.currency === "cny";

  const create = async () => {
    setBusy(true);
    try {
      const body = provider === "stripe"
        ? { usd: amount, provider: "stripe" }
        : { usd: amount, method: "alipay" };
      const r = await apiPost<any>("/billing/topup", body);
      setOrder(r);
      if (provider !== "stripe") {
        // QR rail settles asynchronously — start polling immediately.
        setPolling(true);
        pollOrder(r.out_trade_no);
      }
      // Stripe rail: wait for the PaymentElement to confirm before polling.
    } catch (e: any) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const pollOrder = async (out: string) => {
    setPolling(true);
    for (let i = 0; i < 90; i++) {
      await new Promise((r) => setTimeout(r, 2000));
      try {
        const r = await apiGet<any>(`/billing/orders/${out}`);
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

  const isStripeOrder = order && order.provider === "stripe";

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
                <Input id="usd" type="number" min="1" max="1000" step="1" value={usd} onChange={(e) => setUsd(e.target.value)} className="font-mono text-2xl" />
              </div>
              <div className="grid grid-cols-3 gap-2">
                {PRESETS.map((p) => (
                  <Button key={p} type="button" variant={amount === p ? "default" : "outline"} size="sm" onClick={() => setUsd(String(p))}>${p}</Button>
                ))}
              </div>

              {/* Rail selector — only shown when both rails are available. */}
              {stripeEnabled && qrEnabled && (
                <div className="grid grid-cols-2 gap-2">
                  <RailButton
                    active={provider === "stripe"}
                    onClick={() => setProvider("stripe")}
                    icon={<CreditCard className="h-4 w-4" />}
                    title={t("billing.provider.stripe")}
                    sub={t("billing.provider.stripeSub")}
                  />
                  <RailButton
                    active={provider === "qr"}
                    onClick={() => setProvider("qr")}
                    icon={<QrCode className="h-4 w-4" />}
                    title={t("billing.provider.qr")}
                    sub={t("billing.provider.qrSub")}
                  />
                </div>
              )}

              {provider === "stripe" ? (
                <div className="space-y-3">
                  <MethodChips />
                  <div className="rounded-md border border-border-strong bg-muted/30 p-3 text-sm">
                    {stripeCNY ? (
                      <>
                        <div className="flex justify-between"><span className="text-muted-foreground">{t("billing.dialog.youPay")}</span><span className="font-mono tabular-nums">¥{(amount * (rate?.cny_per_usd || 7.2)).toFixed(2)}</span></div>
                        <div className="flex justify-between"><span className="text-muted-foreground">{t("billing.dialog.walletCredit")}</span><span className="font-mono tabular-nums">{fmtUSD(amount)}</span></div>
                        <div className="flex justify-between text-xs">
                          <span className="text-muted-foreground inline-flex items-center gap-1.5">
                            <span className="relative inline-flex h-1.5 w-1.5">
                              <span className="absolute inline-flex h-full w-full rounded-full bg-emerald-500 opacity-75 animate-ping" />
                              <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-emerald-500" />
                            </span>
                            {t("billing.dialog.liveRate")}{rateAge > 1 ? t("billing.dialog.rateSecondsAgo", { n: rateAge }) : ""}
                          </span>
                          <span className="font-mono text-muted-foreground">{t("billing.dialog.ratePrefix")}{rate?.cny_per_usd.toFixed(4)}</span>
                        </div>
                      </>
                    ) : (
                      <>
                        <div className="flex justify-between"><span className="text-muted-foreground">{t("billing.stripe.youPay")}</span><span className="font-mono tabular-nums">{fmtUSD(amount)}</span></div>
                        <div className="flex justify-between"><span className="text-muted-foreground">{t("billing.dialog.walletCredit")}</span><span className="font-mono tabular-nums">{fmtUSD(amount)}</span></div>
                        <div className="mt-1 text-[11px] text-muted-foreground">{t("billing.stripe.usdNote")}</div>
                      </>
                    )}
                  </div>
                </div>
              ) : (
                <>
                  <AlipayBadge />
                  <div className="rounded-md border border-border-strong bg-muted/30 p-3 text-sm">
                    <div className="flex justify-between"><span className="text-muted-foreground">{t("billing.dialog.youPay")}</span><span className="font-mono tabular-nums">¥{(amount * (rate?.cny_per_usd || 7.2)).toFixed(2)}</span></div>
                    <div className="flex justify-between"><span className="text-muted-foreground">{t("billing.dialog.walletCredit")}</span><span className="font-mono tabular-nums">{fmtUSD(amount)}</span></div>
                    <div className="flex justify-between text-xs">
                      <span className="text-muted-foreground inline-flex items-center gap-1.5">
                        <span className="relative inline-flex h-1.5 w-1.5">
                          <span className="absolute inline-flex h-full w-full rounded-full bg-emerald-500 opacity-75 animate-ping" />
                          <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-emerald-500" />
                        </span>
                        {t("billing.dialog.liveRate")}{rateAge > 1 ? t("billing.dialog.rateSecondsAgo", { n: rateAge }) : ""}
                      </span>
                      <span className="font-mono text-muted-foreground">{t("billing.dialog.ratePrefix")}{rate?.cny_per_usd.toFixed(4)}</span>
                    </div>
                  </div>
                </>
              )}
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={close}>{t("billing.dialog.cancel")}</Button>
              <Button onClick={create} disabled={busy || !(amount >= 1) || !providers}>{busy ? t("billing.dialog.creating") : t("billing.dialog.create")}</Button>
            </DialogFooter>
          </>
        ) : paid ? (
          <PaidState usd={order.usd_credit} />
        ) : isStripeOrder ? (
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
                  returnUrl={order.return_url || ""}
                  outTradeNo={order.out_trade_no}
                  amountUsd={order.usd_credit}
                  payLabel={order.currency === "cny" ? `¥${Number(order.cny_amount).toFixed(2)}` : fmtUSD(order.usd_credit)}
                  onConfirmed={() => pollOrder(order.out_trade_no)}
                />
              )}
            </div>
            {!polling && (
              <DialogFooter>
                <Button variant="outline" onClick={close}>{t("billing.dialog.cancel")}</Button>
              </DialogFooter>
            )}
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t("billing.dialog.payTitle")}</DialogTitle>
              <DialogDescription>
                {order.pay_url ? t("billing.dialog.payDescWithUrl") : t("billing.dialog.payDescNoUrl")}
              </DialogDescription>
            </DialogHeader>
            <div className="flex flex-col items-center gap-4 py-4">
              <AlipayBadge />
              <div className="rounded-lg border border-border-strong bg-white p-4">
                {order.img ? (
                  <img src={order.img} alt="Alipay QR" width={220} height={220} className="block" />
                ) : (
                  <QRCodeSVG value={order.pay_url || order.qr_code} size={220} level="M" />
                )}
              </div>
              <div className="text-center">
                <div className="font-mono text-2xl font-semibold tabular-nums">¥{order.cny_amount.toFixed(2)}</div>
                <div className="text-sm text-muted-foreground">{t("billing.dialog.walletCreditApprox", { n: order.usd_credit })}</div>
                <div className="mt-1 inline-flex items-center gap-1.5 text-[11px] text-muted-foreground font-mono">
                  <Lock className="h-3 w-3" />
                  {t("billing.dialog.rateLocked", { rate: order.rate.toFixed(4) })}
                </div>
              </div>
              {order.pay_url && (
                <a href={order.pay_url} target="_blank" rel="noreferrer noopener" className="inline-flex items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow hover:bg-primary/90">
                  {t("billing.dialog.openInAlipay")}
                </a>
              )}
              <div className="text-xs text-muted-foreground">{polling ? t("billing.dialog.waiting") : t("billing.dialog.timeout")}</div>
              <code className="text-xs text-muted-foreground">{order.out_trade_no}</code>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={close}>{t("common.close")}</Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function RailButton({ active, onClick, icon, title, sub }: { active: boolean; onClick: () => void; icon: React.ReactNode; title: string; sub: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition ${active ? "border-primary bg-primary/[0.06] ring-1 ring-primary/30" : "border-border-strong hover:border-primary/40"}`}
    >
      <span className={`inline-flex items-center gap-2 text-sm font-medium ${active ? "text-primary" : "text-foreground"}`}>{icon}{title}</span>
      <span className="text-[11px] text-muted-foreground">{sub}</span>
    </button>
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
        <span key={m.label} className={`rounded-md border border-border-strong bg-muted/30 px-2 py-1 text-[11px] font-medium ${m.cls}`}>{m.label}</span>
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
      <div className="font-mono text-2xl font-semibold tabular-nums text-primary">+{fmtUSD(usd)}</div>
    </div>
  );
}

function AlipayBadge() {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-2.5 rounded-md border border-[#1677FF]/30 bg-[#1677FF]/[0.06] px-3 py-2">
      <span
        aria-hidden="true"
        className="grid h-6 w-6 shrink-0 place-items-center rounded-md bg-[#1677FF] font-display text-sm font-semibold text-white"
        style={{ fontFamily: '"PingFang SC", "Microsoft YaHei", "Hiragino Sans GB", sans-serif' }}
      >
        支
      </span>
      <div className="flex min-w-0 flex-col leading-tight">
        <span className="text-sm font-semibold text-foreground">Alipay 支付宝</span>
        <span className="text-[11px] text-muted-foreground">{t("billing.dialog.onlySupported")}</span>
      </div>
    </div>
  );
}
