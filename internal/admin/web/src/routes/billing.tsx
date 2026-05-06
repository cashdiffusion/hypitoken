import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { QRCodeSVG } from "qrcode.react";
import { Wallet, RefreshCw, Lock } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { apiGet, apiPost } from "@/lib/api";
import { useAuth } from "@/hooks/use-auth";
import { fmtUSD } from "@/lib/utils";
import type { AlipayOrder, ExchangeRate, WalletTx } from "@/lib/types";

const PRESETS = [5, 10, 20, 50, 100, 200];

export default function BillingPage() {
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

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="font-display text-3xl font-semibold tracking-tight">Billing</h1>
          <p className="text-muted-foreground">Top up your wallet via Alipay.</p>
        </div>
        <Button onClick={reload} variant="outline" size="sm" className="gap-2">
          <RefreshCw className="h-3.5 w-3.5" /> Refresh
        </Button>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Card className="border-primary/40 bg-primary/[0.04]">
          <CardHeader>
            <CardDescription>Current balance</CardDescription>
            <CardTitle className="font-mono text-4xl font-semibold tabular-nums tracking-tight">{fmtUSD(user?.balance_usd)}</CardTitle>
          </CardHeader>
          <CardContent>
            <Button onClick={() => setOpen(true)} size="lg" className="gap-2"><Wallet className="h-4 w-4" /> Top up</Button>
            {rate && <p className="mt-3 text-xs text-muted-foreground">Live rate: 1 USD = ¥{rate.cny_per_usd.toFixed(4)}</p>}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardDescription>Lifetime usage</CardDescription>
            <CardTitle className="font-mono text-4xl font-semibold tabular-nums tracking-tight">{fmtUSD(tx.filter(t => t.kind === "charge").reduce((s, t) => s + Math.abs(t.amount_usd), 0))}</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">{tx.filter(t => t.kind === "charge").length} requests billed · {tx.filter(t => t.kind === "topup").length} top-ups</p>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Top-up orders</CardTitle>
          <CardDescription>Pending and recently paid Alipay orders. Expired orders are hidden.</CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          {(() => {
            // expired/failed orders are noise once they're past their TTL —
            // hide them so the user only sees what's actionable (still
            // payable) or recent receipts.
            const visible = orders.filter((o) => o.status === "pending" || o.status === "paid");
            if (visible.length === 0) return <div className="p-8 text-center text-sm text-muted-foreground">No active orders.</div>;
            return (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Order</TableHead>
                  <TableHead className="text-right">USD</TableHead>
                  <TableHead className="text-right">CNY</TableHead>
                  <TableHead className="text-right">Rate</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Created</TableHead>
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
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Wallet history</CardTitle>
          <CardDescription>
            Top-ups, refunds and admin adjustments only.{" "}
            Per-request charges are listed under <Link to="/app/logs" className="underline underline-offset-2 text-foreground hover:text-primary">Logs</Link> with token-level precision.
          </CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          {(() => {
            // Charge events (one row per API request) live in the Logs
            // page where they belong with full token / pricing detail.
            // Here we want the wallet-level financial picture only:
            // top-ups, refunds, and admin adjustments.
            const visible = tx.filter((t) => t.kind !== "charge");
            if (visible.length === 0) return <div className="p-8 text-center text-sm text-muted-foreground">No wallet activity yet.</div>;
            return (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Kind</TableHead>
                  <TableHead className="text-right">Amount</TableHead>
                  <TableHead>Reference</TableHead>
                  <TableHead>When</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.slice(0, 50).map((t) => (
                  <TableRow key={t.id}>
                    <TableCell className="capitalize font-medium">{t.kind}</TableCell>
                    <TableCell className={`font-mono tabular-nums text-right ${t.amount_usd >= 0 ? "text-success" : ""}`}>{t.amount_usd >= 0 ? "+" : ""}{fmtUSD(t.amount_usd)}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">{t.ref || "—"}</TableCell>
                    <TableCell className="text-muted-foreground">{new Date(t.created_at * 1000).toLocaleString()}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            );
          })()}
        </CardContent>
      </Card>

      <TopUpDialog open={open} onOpenChange={setOpen} rate={rate} onPaid={async () => { await reload(); await refresh(); }} />
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const cls =
    status === "paid" ? "bg-success/15 text-success border-success/30"
    : status === "pending" ? "bg-warning/15 text-warning border-warning/30"
    : "bg-destructive/15 text-destructive border-destructive/30";
  return <span className={`inline-flex rounded border px-2 py-0.5 text-xs font-mono uppercase tracking-wider ${cls}`}>{status}</span>;
}

function TopUpDialog({ open, onOpenChange, rate: initialRate, onPaid }: any) {
  const [usd, setUsd] = useState("10");
  // Only Alipay rail is wired up on the merchant side. The backend still
  // accepts method=wxpay if a future operator enables WeChat — but UI-side
  // we don't expose it because it would just error from the gateway.
  const method = "alipay";
  const [order, setOrder] = useState<any>(null);
  const [busy, setBusy] = useState(false);
  const [polling, setPolling] = useState(false);
  // Live rate refresh while the dialog is open. Pre-create the user sees
  // a current quote; once the order is created the rate is locked into
  // order.rate and we display that instead.
  const [liveRate, setLiveRate] = useState<ExchangeRate | null>(initialRate);
  const [rateAge, setRateAge] = useState(0);
  useEffect(() => {
    if (!open || order) return;
    let cancelled = false;
    const tick = async () => {
      try {
        const r = await apiGet<ExchangeRate>("/billing/rate");
        if (!cancelled) {
          setLiveRate(r);
          setRateAge(0);
        }
      } catch {}
    };
    tick();
    const fetchT = setInterval(tick, 30_000);
    const ageT = setInterval(() => setRateAge((a) => a + 1), 1000);
    return () => { cancelled = true; clearInterval(fetchT); clearInterval(ageT); };
  }, [open, order]);
  const rate = liveRate;

  const create = async () => {
    setBusy(true);
    try {
      const r = await apiPost<any>("/billing/topup", { usd: parseFloat(usd), method });
      setOrder(r);
      setPolling(true);
      pollOrder(r.out_trade_no);
    } catch (e: any) {
      toast.error(e.message);
    } finally {
      setBusy(false);
    }
  };

  const pollOrder = async (out: string) => {
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 2000));
      try {
        const r = await apiGet<any>(`/billing/orders/${out}`);
        if (r.status === "paid") {
          toast.success(`Wallet credited $${r.usd_credit}`);
          setPolling(false);
          onPaid();
          onOpenChange(false);
          setOrder(null);
          setUsd("10");
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
  };

  return (
    <Dialog open={open} onOpenChange={(v) => (!v ? close() : onOpenChange(true))}>
      <DialogContent className="sm:max-w-[500px]">
        {!order ? (
          <>
            <DialogHeader>
              <DialogTitle>Top up wallet</DialogTitle>
              <DialogDescription>You'll pay in CNY via Alipay at the live USD/CNY rate. Wallet is credited in USD.</DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-2">
              <AlipayBadge />
              <div className="space-y-2">
                <Label htmlFor="usd">Amount (USD)</Label>
                <Input id="usd" type="number" min="1" max="1000" step="1" value={usd} onChange={(e) => setUsd(e.target.value)} className="font-mono text-2xl" />
              </div>
              <div className="grid grid-cols-3 gap-2">
                {PRESETS.map((p) => (
                  <Button key={p} type="button" variant="outline" size="sm" onClick={() => setUsd(String(p))}>${p}</Button>
                ))}
              </div>
              <div className="rounded-md border border-border-strong bg-muted/30 p-3 text-sm">
                <div className="flex justify-between"><span className="text-muted-foreground">You pay (CNY)</span><span className="font-mono tabular-nums">¥{(parseFloat(usd || "0") * (rate?.cny_per_usd || 7.2)).toFixed(2)}</span></div>
                <div className="flex justify-between"><span className="text-muted-foreground">Wallet credit</span><span className="font-mono tabular-nums">${parseFloat(usd || "0").toFixed(2)}</span></div>
                <div className="flex justify-between text-xs">
                  <span className="text-muted-foreground inline-flex items-center gap-1.5">
                    <span className="relative inline-flex h-1.5 w-1.5">
                      <span className="absolute inline-flex h-full w-full rounded-full bg-emerald-500 opacity-75 animate-ping" />
                      <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-emerald-500" />
                    </span>
                    Live rate{rateAge > 1 ? ` · ${rateAge}s ago` : ""}
                  </span>
                  <span className="font-mono text-muted-foreground">1 USD = ¥{rate?.cny_per_usd.toFixed(4)}</span>
                </div>
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={close}>Cancel</Button>
              <Button onClick={create} disabled={busy || !parseFloat(usd)}>{busy ? "Creating…" : "Create order"}</Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Pay with Alipay 支付宝</DialogTitle>
              <DialogDescription>
                {order.pay_url
                  ? "Open the Alipay app and scan the QR, or click the link to open the payment page."
                  : "Open the Alipay app and scan the QR code."}
              </DialogDescription>
            </DialogHeader>
            <div className="flex flex-col items-center gap-4 py-4">
              <AlipayBadge />
              {/* QR: prefer hosted img URL if upstream provided one (it's the
                  authoritative scannable QR image); otherwise render the
                  pay_url/qr_code as a generated QR — the redirect URL is
                  itself scannable on most payment apps. */}
              <div className="rounded-lg border border-border-strong bg-white p-4">
                {order.img ? (
                  <img src={order.img} alt="Alipay QR" width={220} height={220} className="block" />
                ) : (
                  <QRCodeSVG value={order.pay_url || order.qr_code} size={220} level="M" />
                )}
              </div>
              <div className="text-center">
                <div className="font-mono text-2xl font-semibold tabular-nums">¥{order.cny_amount.toFixed(2)}</div>
                <div className="text-sm text-muted-foreground">≈ ${order.usd_credit} wallet credit</div>
                <div className="mt-1 inline-flex items-center gap-1.5 text-[11px] text-muted-foreground font-mono">
                  <Lock className="h-3 w-3" />
                  Rate locked · 1 USD = ¥{order.rate.toFixed(4)}
                </div>
              </div>
              {order.pay_url && (
                <a
                  href={order.pay_url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex items-center justify-center rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow hover:bg-primary/90"
                >
                  Open in Alipay →
                </a>
              )}
              <div className="text-xs text-muted-foreground">{polling ? "Waiting for payment…" : "Payment timeout"}</div>
              <code className="text-xs text-muted-foreground">{order.out_trade_no}</code>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={close}>Close</Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

// AlipayBadge is the pill we show prominently in the top-up dialog so
// users can see at a glance which payment rail will be used. The wordmark
// uses Alipay's brand blue (#1677FF). The Chinese name "支付宝" is the
// primary identifier for Chinese users; the English "Alipay" mirrors it
// for international users. Logo is the dotted-square treatment from
// Alipay's official brand assets, drawn inline as SVG so we don't ship a
// separate image file.
function AlipayBadge() {
  return (
    <div className="flex items-center gap-2.5 rounded-md border border-[#1677FF]/30 bg-[#1677FF]/[0.06] px-3 py-2">
      {/* Alipay-blue rounded square with the 支 wordmark — close to the
          official lock-up without redistributing the brand SVG. */}
      <span
        aria-hidden="true"
        className="grid h-6 w-6 shrink-0 place-items-center rounded-md bg-[#1677FF] font-display text-sm font-semibold text-white"
        style={{ fontFamily: '"PingFang SC", "Microsoft YaHei", "Hiragino Sans GB", sans-serif' }}
      >
        支
      </span>
      <div className="flex min-w-0 flex-col leading-tight">
        <span className="text-sm font-semibold text-foreground">Alipay 支付宝</span>
        <span className="text-[11px] text-muted-foreground">The only payment method currently supported</span>
      </div>
    </div>
  );
}
