// Operator referral-ops dashboard. Talks only to /api/v2/admin/referral/*:
// the viral funnel + K-factor + ROI + gift stats + top referrers, plus
// campaign configuration (bonus amounts, expiry, caps, A/B copy, status).
import { Award, Gift, TrendingUp, Users } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { GlassPanel } from "@/components/app/page-primitives";
import { Button } from "@/components/ui/button";
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
import { apiGet, apiPatch } from "@/lib/api";
import type { ReferralCampaign, ReferralOpsStats } from "@/lib/types";
import { errMsg, fmtUSD } from "@/lib/utils";

function Stat({ icon: Icon, label, value }: { icon: typeof Gift; label: string; value: string }) {
  return (
    <GlassPanel className="flex items-center gap-3 p-4">
      <Icon className="size-5 text-primary" />
      <div>
        <div className="text-xs uppercase tracking-wider text-muted-foreground">{label}</div>
        <div className="text-xl font-semibold">{value}</div>
      </div>
    </GlassPanel>
  );
}

export function ReferralTab() {
  const { t } = useTranslation();
  const [stats, setStats] = useState<ReferralOpsStats | null>(null);
  const [campaigns, setCampaigns] = useState<ReferralCampaign[]>([]);
  const [suspended, setSuspended] = useState(false);

  const load = () => {
    apiGet<{ stats: ReferralOpsStats; suspended: boolean }>("/admin/referral/analytics")
      .then((r) => {
        setStats(r.stats);
        setSuspended(r.suspended);
      })
      .catch(() => {});
    apiGet<{ campaigns: ReferralCampaign[] }>("/admin/referral/campaigns")
      .then((r) => setCampaigns(r.campaigns || []))
      .catch(() => {});
  };
  // biome-ignore lint/correctness/useExhaustiveDependencies: run once on mount
  useEffect(() => {
    load();
  }, []);

  const g = stats?.gift_totals;

  return (
    <div className="space-y-8">
      {suspended && (
        <div className="rounded-xl border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-600 dark:text-amber-400">
          {t("adminReferral.suspended")}
        </div>
      )}
      {stats && stats.daily_budget_usd > 0 && (
        <div
          className={
            stats.budget_tripped
              ? "flex items-center justify-between rounded-xl border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-500"
              : "flex items-center justify-between rounded-xl border border-emerald-500/30 bg-emerald-500/5 px-4 py-3 text-sm"
          }
        >
          <span>
            {stats.budget_tripped
              ? t("adminReferral.breakerTripped")
              : t("adminReferral.breakerOk")}
          </span>
          <span className="font-mono text-xs">
            {fmtUSD(stats.today_bonus_usd)} / {fmtUSD(stats.daily_budget_usd)}
          </span>
        </div>
      )}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat
          icon={Users}
          label={t("adminReferral.conversions")}
          value={String(stats?.conversions ?? 0)}
        />
        <Stat
          icon={TrendingUp}
          label={t("adminReferral.kFactor")}
          value={(stats?.k_factor ?? 0).toFixed(2)}
        />
        <Stat
          icon={Award}
          label={t("adminReferral.spend")}
          value={fmtUSD(stats?.platform_spend ?? 0)}
        />
        <Stat
          icon={Users}
          label={t("adminReferral.fraudBlocked")}
          value={String(stats?.fraud_blocked ?? 0)}
        />
        <Stat
          icon={Gift}
          label={t("adminReferral.cardsMinted")}
          value={String(stats?.cards_minted ?? 0)}
        />
        <Stat
          icon={Gift}
          label={t("adminReferral.giftsClaimed")}
          value={`${g?.claimed_count ?? 0} · ${fmtUSD(g?.claimed_usd ?? 0)}`}
        />
        <Stat
          icon={Gift}
          label={t("adminReferral.giftsPending")}
          value={`${g?.pending_count ?? 0} · ${fmtUSD(g?.pending_usd ?? 0)}`}
        />
        <Stat
          icon={Gift}
          label={t("adminReferral.giftsRefunded")}
          value={`${g?.refunded_count ?? 0} · ${fmtUSD(g?.refunded_usd ?? 0)}`}
        />
      </div>

      <GlassPanel className="p-5">
        <h3 className="mb-3 text-sm font-medium">{t("adminReferral.topReferrers")}</h3>
        {stats && stats.top_referrers.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>#</TableHead>
                <TableHead>{t("adminReferral.user")}</TableHead>
                <TableHead className="text-right">{t("adminReferral.invites")}</TableHead>
                <TableHead className="text-right">{t("adminReferral.earned")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {stats.top_referrers.map((r, i) => (
                <TableRow key={r.user_id}>
                  <TableCell>{i + 1}</TableCell>
                  <TableCell className="font-mono text-xs">{r.email || `#${r.user_id}`}</TableCell>
                  <TableCell className="text-right">{r.invites}</TableCell>
                  <TableCell className="text-right">{fmtUSD(r.earned_usd)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-sm text-muted-foreground">{t("adminReferral.noReferrers")}</p>
        )}
      </GlassPanel>

      <div className="space-y-4">
        <h3 className="text-sm font-medium">{t("adminReferral.campaigns")}</h3>
        {campaigns.map((c) => (
          <CampaignEditor key={c.id} campaign={c} onSaved={load} />
        ))}
      </div>
    </div>
  );
}

function CampaignEditor({
  campaign,
  onSaved,
}: {
  campaign: ReferralCampaign;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [status, setStatus] = useState(campaign.status || "active");
  const [invitee, setInvitee] = useState(String(campaign.invitee_bonus_usd));
  const [inviter, setInviter] = useState(String(campaign.inviter_bonus_usd));
  const [expiry, setExpiry] = useState(String(campaign.gift_expiry_days));
  const [maxGift, setMaxGift] = useState(String(campaign.max_gift_usd));
  const [dailyBudget, setDailyBudget] = useState(String(campaign.daily_budget_usd ?? 0));
  const [headline, setHeadline] = useState(campaign.headline);
  const [subcopy, setSubcopy] = useState(campaign.subcopy);
  const [saving, setSaving] = useState(false);

  const curStatus = status || "active";

  const save = async (nextStatus?: string) => {
    setSaving(true);
    try {
      await apiPatch(`/admin/referral/campaigns/${campaign.id}`, {
        name: campaign.name,
        kind: campaign.kind,
        status: nextStatus || curStatus,
        invitee_bonus_usd: Number(invitee),
        inviter_bonus_usd: Number(inviter),
        gift_expiry_days: Number(expiry),
        max_gift_usd: Number(maxGift),
        daily_budget_usd: Number(dailyBudget),
        headline,
        subcopy,
      });
      if (nextStatus) setStatus(nextStatus);
      toast.success(t("adminReferral.saved"));
      onSaved();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <GlassPanel className="space-y-4 p-5">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="font-medium">{campaign.name}</span>
          <span className="rounded-full bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
            {campaign.slug}
          </span>
          <span
            className={
              curStatus === "active"
                ? "rounded-full bg-emerald-500/15 px-2 py-0.5 text-xs text-emerald-500"
                : "rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground"
            }
          >
            {curStatus}
          </span>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => save(curStatus === "active" ? "paused" : "active")}
          disabled={saving}
        >
          {curStatus === "active" ? t("adminReferral.pause") : t("adminReferral.activate")}
        </Button>
      </div>
      <div className="grid gap-3 sm:grid-cols-5">
        <Field label={t("adminReferral.inviteeBonus")} value={invitee} onChange={setInvitee} />
        <Field label={t("adminReferral.inviterBonus")} value={inviter} onChange={setInviter} />
        <Field label={t("adminReferral.giftExpiry")} value={expiry} onChange={setExpiry} />
        <Field label={t("adminReferral.maxGift")} value={maxGift} onChange={setMaxGift} />
        <Field
          label={t("adminReferral.dailyBudget")}
          value={dailyBudget}
          onChange={setDailyBudget}
        />
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label>{t("adminReferral.headline")}</Label>
          <Input value={headline} onChange={(e) => setHeadline(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label>{t("adminReferral.subcopy")}</Label>
          <Input value={subcopy} onChange={(e) => setSubcopy(e.target.value)} />
        </div>
      </div>
      {campaign.tiers.length > 0 && (
        <div className="flex flex-wrap gap-2 pt-1">
          {campaign.tiers.map((tier) => (
            <span
              key={tier.id}
              className="rounded-full border border-border px-2.5 py-1 text-xs text-muted-foreground"
            >
              {tier.tier_name} · {tier.threshold}邀 · {fmtUSD(tier.bonus_usd)}
            </span>
          ))}
        </div>
      )}
      <Button onClick={() => save()} disabled={saving} size="sm">
        {saving ? t("adminReferral.saving") : t("adminReferral.save")}
      </Button>
    </GlassPanel>
  );
}

function Field({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      <Input type="number" value={value} onChange={(e) => onChange(e.target.value)} />
    </div>
  );
}
