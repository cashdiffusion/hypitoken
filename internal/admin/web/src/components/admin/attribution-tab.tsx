// Marketing-channel attribution tab for the admin panel. Self-contained: it
// talks only to the /api/v2/admin/growth/* endpoints exposed by the backend
// internal/saas/growth module — channel CRUD plus a visits/conversion/ROI
// analytics rollup. Kept in its own file so the growth feature stays easy to
// find and maintain end-to-end (backend package + this tab).

import { Copy, Megaphone, Pencil, Plus, Trash2, TrendingUp, UserPlus, Wallet } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { FadeIn } from "@/components/admin/fade-in";
import { VisitorBehaviorSection } from "@/components/admin/visitor-behavior-section";
import { GlassPanel } from "@/components/app/page-primitives";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm-dialog";
import {
  Dialog,
  DialogContent,
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
import { apiDelete, apiGet, apiPatch, apiPost } from "@/lib/api";
import { cn, errMsg, fmtInt, fmtUSD } from "@/lib/utils";

interface Channel {
  id: number;
  slug: string;
  name: string;
  description: string;
  bonus_usd: number;
  enabled: boolean;
  created_at: number;
  updated_at: number;
}

interface ChannelStat {
  slug: string;
  name: string;
  enabled: boolean;
  bonus_usd: number;
  visitors: number;
  signups: number;
  conversion: number;
  avg_dwell_ms: number;
  bonus_paid: number;
  topped_up_usd: number;
  spent_usd: number;
}

interface DailyPoint {
  day: string;
  visitors: number;
  signups: number;
}

interface Totals {
  channels: number;
  visitors: number;
  signups: number;
  bonus_paid: number;
}

interface Analytics {
  totals: Totals;
  channels: ChannelStat[];
  daily: DailyPoint[];
  // True while saas.referrals_enabled is off: channels still list and their
  // history still reads, but ?ref= links no longer track and no bonus is
  // granted, so edits here have no effect until the programme is resumed.
  suspended?: boolean;
}

// referralLink builds the shareable homepage link for a channel slug.
function referralLink(slug: string): string {
  return `${window.location.origin}/?ref=${slug}`;
}

function fmtPct(r: number): string {
  return `${(r * 100).toFixed(1)}%`;
}

function fmtDwell(ms: number): string {
  if (!ms || ms <= 0) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return rem ? `${m}m ${rem}s` : `${m}m`;
}

export function AttributionTab() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const [channels, setChannels] = useState<Channel[]>([]);
  const [analytics, setAnalytics] = useState<Analytics | null>(null);
  const [editing, setEditing] = useState<Channel | null>(null);
  const [creating, setCreating] = useState(false);

  const reload = async () => {
    try {
      const [chRes, anRes] = await Promise.all([
        apiGet<{ channels: Channel[] }>("/admin/growth/channels"),
        apiGet<Analytics>("/admin/growth/analytics?days=14"),
      ]);
      setChannels(chRes.channels || []);
      setAnalytics(anRes);
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: load once on mount; reload runs imperatively after mutations.
  useEffect(() => {
    reload();
  }, []);

  const statFor = (slug: string): ChannelStat | undefined =>
    analytics?.channels.find((c) => c.slug === slug);

  const totals = analytics?.totals;

  return (
    <FadeIn>
      <div className="space-y-6">
        {analytics?.suspended && (
          <div className="rounded-xl border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-600 dark:text-amber-400">
            {t("admin.growth.suspended")}
          </div>
        )}
        {/* headline totals */}
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatTile
            icon={<Megaphone className="h-4 w-4" />}
            label={t("admin.growth.totals.channels")}
            value={fmtInt(totals?.channels ?? 0)}
          />
          <StatTile
            icon={<TrendingUp className="h-4 w-4" />}
            label={t("admin.growth.totals.visitors")}
            value={fmtInt(totals?.visitors ?? 0)}
          />
          <StatTile
            icon={<UserPlus className="h-4 w-4" />}
            label={t("admin.growth.totals.signups")}
            value={fmtInt(totals?.signups ?? 0)}
          />
          <StatTile
            icon={<Wallet className="h-4 w-4" />}
            label={t("admin.growth.totals.bonusPaid")}
            value={fmtUSD(totals?.bonus_paid ?? 0)}
          />
        </div>

        {/* trend chart */}
        <GlassPanel title={t("admin.growth.chartTitle")}>
          <TrendChart data={analytics?.daily ?? []} />
        </GlassPanel>

        {/* channel manager */}
        <GlassPanel
          title={t("admin.growth.heading")}
          action={
            <Button onClick={() => setCreating(true)}>
              <Plus className="mr-1.5 h-4 w-4" />
              {t("admin.growth.newBtn")}
            </Button>
          }
          bodyClassName="p-0"
        >
          <p className="px-5 pt-3 text-xs text-muted-foreground">{t("admin.growth.sub")}</p>
          {channels.length === 0 ? (
            <div className="grid h-28 place-items-center text-sm text-muted-foreground">
              {t("admin.growth.noChannels")}
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("admin.growth.cols.channel")}</TableHead>
                    <TableHead>{t("admin.growth.cols.link")}</TableHead>
                    <TableHead className="text-right">{t("admin.growth.cols.bonus")}</TableHead>
                    <TableHead className="text-right">{t("admin.growth.cols.visitors")}</TableHead>
                    <TableHead className="text-right">{t("admin.growth.cols.signups")}</TableHead>
                    <TableHead className="text-right">
                      {t("admin.growth.cols.conversion")}
                    </TableHead>
                    <TableHead className="text-right">{t("admin.growth.cols.dwell")}</TableHead>
                    <TableHead className="text-right">{t("admin.growth.cols.cost")}</TableHead>
                    <TableHead className="text-right">{t("admin.growth.cols.roi")}</TableHead>
                    <TableHead></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {channels.map((ch) => {
                    const s = statFor(ch.slug);
                    return (
                      <TableRow key={ch.id}>
                        <TableCell>
                          <div className="flex items-center gap-2 font-medium">
                            {ch.name || ch.slug}
                            <Badge variant={ch.enabled ? "default" : "secondary"}>
                              {ch.enabled ? t("admin.growth.enabled") : t("admin.growth.disabled")}
                            </Badge>
                          </div>
                          {ch.description && (
                            <div className="text-xs text-muted-foreground">{ch.description}</div>
                          )}
                        </TableCell>
                        <TableCell>
                          <CopyLink slug={ch.slug} />
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {ch.bonus_usd > 0 ? fmtUSD(ch.bonus_usd) : "—"}
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {fmtInt(s?.visitors ?? 0)}
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {fmtInt(s?.signups ?? 0)}
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {s && s.visitors > 0 ? fmtPct(s.conversion) : "—"}
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums text-muted-foreground">
                          {fmtDwell(s?.avg_dwell_ms ?? 0)}
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums text-amber-600 dark:text-amber-400">
                          {s && s.bonus_paid > 0 ? `-${fmtUSD(s.bonus_paid)}` : "—"}
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums">
                          <span className="text-emerald-600 dark:text-emerald-400">
                            {fmtUSD(s?.topped_up_usd ?? 0)}
                          </span>
                          <span className="text-muted-foreground"> / </span>
                          <span>{fmtUSD(s?.spent_usd ?? 0)}</span>
                        </TableCell>
                        <TableCell className="text-right whitespace-nowrap">
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => setEditing(ch)}
                            aria-label={t("common.edit")}
                          >
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            className="text-destructive"
                            aria-label={t("common.delete")}
                            onClick={async () => {
                              if (
                                !(await confirm({
                                  title: t("common.delete"),
                                  description: t("admin.growth.confirmDelete", {
                                    name: ch.name || ch.slug,
                                  }),
                                  confirmLabel: t("common.delete"),
                                  destructive: true,
                                }))
                              )
                                return;
                              try {
                                await apiDelete(`/admin/growth/channels/${ch.id}`);
                                toast.success(t("admin.growth.deleted"));
                                reload();
                              } catch (e) {
                                toast.error(errMsg(e));
                              }
                            }}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </GlassPanel>

        {/* site-wide visitor behaviour — all homepage visitors, not just ?ref= */}
        <VisitorBehaviorSection />
      </div>

      {creating && (
        <ChannelDialog
          mode="create"
          onClose={() => setCreating(false)}
          onDone={() => {
            setCreating(false);
            reload();
          }}
        />
      )}
      {editing && (
        <ChannelDialog
          mode="edit"
          channel={editing}
          onClose={() => setEditing(null)}
          onDone={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
    </FadeIn>
  );
}

function StatTile({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <div className="glass rounded-xl p-4">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        {icon}
        {label}
      </div>
      <div className="mt-1.5 font-mono text-2xl font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function CopyLink({ slug }: { slug: string }) {
  const { t } = useTranslation();
  const link = referralLink(slug);
  return (
    <button
      type="button"
      className="group inline-flex max-w-[16rem] items-center gap-1.5 rounded-md border border-border/60 bg-background/40 px-2 py-1 font-mono text-xs text-muted-foreground transition-colors hover:border-primary/50 hover:text-foreground"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(link);
          toast.success(t("admin.growth.copied"));
        } catch {
          toast.error(errMsg(null));
        }
      }}
      title={link}
      aria-label={t("admin.growth.copyLink")}
    >
      <span className="truncate">/?ref={slug}</span>
      <Copy className="h-3 w-3 shrink-0 opacity-60 group-hover:opacity-100" />
    </button>
  );
}

// ChannelDialog handles both create and edit. Slug is editable only on create
// (the backend treats it as immutable so existing links never break).
function ChannelDialog({
  mode,
  channel,
  onClose,
  onDone,
}: {
  mode: "create" | "edit";
  channel?: Channel;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const [slug, setSlug] = useState(channel?.slug ?? "");
  const [name, setName] = useState(channel?.name ?? "");
  const [desc, setDesc] = useState(channel?.description ?? "");
  const [bonus, setBonus] = useState(String(channel?.bonus_usd ?? 0));
  const [enabled, setEnabled] = useState(channel?.enabled ?? true);
  const [busy, setBusy] = useState(false);

  const save = async () => {
    setBusy(true);
    try {
      const body = {
        slug: slug.trim().toLowerCase(),
        name,
        description: desc,
        bonus_usd: Number.parseFloat(bonus) || 0,
        enabled,
      };
      if (mode === "create") {
        await apiPost("/admin/growth/channels", body);
        toast.success(t("admin.growth.created"));
      } else if (channel) {
        await apiPatch(`/admin/growth/channels/${channel.id}`, body);
        toast.success(t("admin.growth.updated"));
      }
      onDone();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {mode === "create" ? t("admin.growth.newTitle") : t("admin.growth.editTitle")}
          </DialogTitle>
        </DialogHeader>
        {mode === "create" && (
          <p className="text-xs text-muted-foreground">{t("admin.growth.newSub")}</p>
        )}
        <div className="grid gap-3 py-2">
          <div className="space-y-2">
            <Label>{t("admin.growth.labels.slug")}</Label>
            <Input
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
              disabled={mode === "edit"}
              placeholder="x, ins, reddit…"
              className="font-mono"
            />
            {mode === "create" && (
              <p className="text-xs text-muted-foreground">{t("admin.growth.labels.slugHint")}</p>
            )}
          </div>
          <div className="space-y-2">
            <Label>{t("admin.growth.labels.name")}</Label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Twitter / X"
            />
          </div>
          <div className="space-y-2">
            <Label>{t("admin.growth.labels.desc")}</Label>
            <Input value={desc} onChange={(e) => setDesc(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>{t("admin.growth.labels.bonus")}</Label>
            <Input
              type="number"
              min="0"
              step="0.5"
              value={bonus}
              onChange={(e) => setBonus(e.target.value)}
              className="font-mono"
            />
          </div>
          <button
            type="button"
            className="flex items-center justify-between rounded-lg border border-border/60 px-3 py-2 text-sm"
            onClick={() => setEnabled((v) => !v)}
          >
            <span>{t("admin.growth.labels.enabled")}</span>
            <span
              className={cn(
                "relative h-5 w-9 rounded-full transition-colors",
                enabled ? "bg-primary" : "bg-muted",
              )}
            >
              <span
                className={cn(
                  "absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition-all",
                  enabled ? "left-[1.125rem]" : "left-0.5",
                )}
              />
            </span>
          </button>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save} disabled={busy || (mode === "create" && !slug.trim())}>
            {mode === "create" ? t("common.create") : t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// TrendChart draws visits (bars) with signups (line + dots) overlaid, in the
// same hand-rolled-SVG idiom as the dashboard's revenue chart — no extra chart
// dependency, scales to the panel width.
function TrendChart({ data }: { data: DailyPoint[] }) {
  const { t } = useTranslation();
  if (data.length === 0 || data.every((d) => d.visitors === 0 && d.signups === 0)) {
    return (
      <div className="grid h-32 place-items-center text-sm text-muted-foreground">
        {t("admin.growth.noData")}
      </div>
    );
  }
  const maxV = Math.max(...data.map((d) => d.visitors), 1);
  const maxS = Math.max(...data.map((d) => d.signups), 1);
  const W = 100;
  const H = 60;
  const slot = W / data.length;
  const barW = slot * 0.55;

  // signups polyline points (scaled to its own axis so a small signup count is
  // still visible against a large visit count).
  const pts = data
    .map((d, i) => {
      const x = i * slot + slot / 2;
      const y = H - (d.signups / maxS) * (H - 6);
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(" ");

  return (
    <div>
      <div className="mb-3 flex gap-4 font-mono text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2 w-3 rounded-sm bg-primary/70" />
          {t("admin.growth.visits")}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2 w-3 rounded-sm bg-emerald-500" />
          {t("admin.growth.signupsLabel")}
        </span>
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        className="block h-32 w-full overflow-visible"
        role="img"
        aria-label={t("admin.growth.chartTitle")}
      >
        <title>{t("admin.growth.chartTitle")}</title>
        <defs>
          <linearGradient id="growthVisitBar" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--primary)" stopOpacity="0.85" />
            <stop offset="100%" stopColor="var(--primary)" stopOpacity="0.25" />
          </linearGradient>
        </defs>
        {data.map((d, i) => {
          const h = (d.visitors / maxV) * (H - 6);
          const x = i * slot + (slot - barW) / 2;
          const y = H - h;
          return (
            <rect
              key={d.day}
              x={x}
              y={y}
              width={barW}
              height={h || 0.5}
              rx="0.6"
              className={cn(d.visitors === 0 && "fill-border")}
              style={d.visitors > 0 ? { fill: "url(#growthVisitBar)" } : undefined}
            >
              <title>{`${d.day}: ${d.visitors} ${t("admin.growth.visits")}, ${d.signups} ${t("admin.growth.signupsLabel")}`}</title>
            </rect>
          );
        })}
        <polyline
          points={pts}
          fill="none"
          stroke="rgb(16 185 129)"
          strokeWidth="1.2"
          strokeLinejoin="round"
          strokeLinecap="round"
          vectorEffect="non-scaling-stroke"
        />
      </svg>
      <div className="mt-2 flex justify-between font-mono text-[10px] text-muted-foreground">
        <span>{data[0]?.day.slice(5)}</span>
        <span>{data[Math.floor(data.length / 2)]?.day.slice(5)}</span>
        <span>{data[data.length - 1]?.day.slice(5)}</span>
      </div>
    </div>
  );
}
