import {
  Award,
  Check,
  Copy,
  Download,
  Gift,
  Link2,
  Lock,
  Send,
  Share2,
  Sparkles,
  Ticket,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { CountUp, GlassPanel, PageHeader, StatTile } from "@/components/app/page-primitives";
import {
  type CardStyle,
  type CardTone,
  TokenCard,
  type TokenCardProps,
  tokenCardPNG,
  tokenCardSVG,
} from "@/components/giftcard/token-card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { useAuth } from "@/hooks/use-auth";
import { apiGet, apiPatch, apiPost } from "@/lib/api";
import type { GiftCard, ReferralMe } from "@/lib/types";
import { cn, errMsg, fmtUSD } from "@/lib/utils";

const STYLE_OPTS: { style: CardStyle; tone: CardTone; label: string }[] = [
  { style: "claude", tone: "dark", label: "Clay Nocturne" },
  { style: "claude", tone: "light", label: "Cream Atelier" },
  { style: "openai", tone: "dark", label: "Midnight Codex" },
  { style: "openai", tone: "light", label: "Platinum Codex" },
];

// absUrl resolves a possibly-relative invite link (the backend emits "/?ref=…"
// when saas.site_url is unset) against the current origin, so a shared link
// always carries a host.
function absUrl(u: string): string {
  if (!u) return u;
  if (/^https?:\/\//i.test(u)) return u;
  if (typeof window !== "undefined") return window.location.origin + u;
  return u;
}

function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

export default function InvitePage() {
  const { t } = useTranslation();
  const { refresh } = useAuth();
  const [params] = useSearchParams();
  const [me, setMe] = useState<ReferralMe | null>(null);
  const [tab, setTab] = useState(params.get("gift") ? "received" : "invite");

  const load = () =>
    apiGet<ReferralMe>("/referral/me")
      .then(setMe)
      .catch((e) => toast.error(errMsg(e)));

  // biome-ignore lint/correctness/useExhaustiveDependencies: run once on mount
  useEffect(() => {
    load();
    // beacon a share impression on open
    apiPost("/referral/cards/0/impression").catch(() => {});
  }, []);

  const stats = me?.stats;
  const tierName = stats?.current_tier?.tier_name || "MEMBER";

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow={t("invite.eyebrow")}
        title={me?.campaign?.headline || t("invite.title")}
        sub={me?.campaign?.subcopy || t("invite.subtitle")}
      />

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <StatTile
          icon={Ticket}
          label={t("invite.stats.invites")}
          value={<CountUp value={stats?.invites || 0} />}
        />
        <StatTile
          icon={Award}
          label={t("invite.stats.earned")}
          value={fmtUSD(stats?.earned_usd || 0)}
        />
        <StatTile
          icon={Sparkles}
          label={t("invite.stats.tier")}
          value={<span className="text-2xl font-semibold tracking-wide">{tierName}</span>}
        />
        <StatTile
          icon={Award}
          label={t("invite.stats.rank")}
          value={stats?.rank ? `#${stats.rank}` : "—"}
        />
      </div>

      {stats?.next_tier && (
        <GlassPanel className="p-5">
          <div className="flex items-center justify-between text-sm">
            <span className="text-muted-foreground">
              {t("invite.nextTier", {
                count: stats.next_remaining,
                tier: stats.next_tier.tier_name,
                bonus: fmtUSD(stats.next_tier.bonus_usd),
              })}
            </span>
            <span className="font-mono text-xs text-muted-foreground">
              {stats.invites}/{stats.next_tier.threshold}
            </span>
          </div>
          <div className="mt-2 h-2 overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full bg-primary transition-all"
              style={{
                width: `${Math.min(100, (stats.invites / Math.max(1, stats.next_tier.threshold)) * 100)}%`,
              }}
            />
          </div>
        </GlassPanel>
      )}

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="invite">
            <Link2 className="mr-1.5 size-4" /> {t("invite.tabs.invite")}
          </TabsTrigger>
          <TabsTrigger value="gift">
            <Gift className="mr-1.5 size-4" /> {t("invite.tabs.gift")}
          </TabsTrigger>
          <TabsTrigger value="received">
            <Ticket className="mr-1.5 size-4" /> {t("invite.tabs.received")}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="invite" className="mt-6">
          {me && <InviteTab me={me} reload={load} tierName={tierName} />}
        </TabsContent>
        <TabsContent value="gift" className="mt-6">
          {me && <GiftTab me={me} onSent={() => refresh()} />}
        </TabsContent>
        <TabsContent value="received" className="mt-6">
          <ReceivedTab initialCode={params.get("gift") || ""} onClaim={() => refresh()} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

// ── Invite tab ──────────────────────────────────────────────────────────────

function InviteTab({
  me,
  reload,
  tierName,
}: {
  me: ReferralMe;
  reload: () => void;
  tierName: string;
}) {
  const { t } = useTranslation();
  const inviteUrl = absUrl(me.invite_url);
  const [style, setStyle] = useState<CardStyle>(me.card.card_style);
  const [tone, setTone] = useState<CardTone>(me.card.card_tone);
  const [tagline, setTagline] = useState(me.card.tagline || t("invite.defaultTagline"));
  const [message, setMessage] = useState(me.card.message || "");
  const [copied, setCopied] = useState(false);
  const [saving, setSaving] = useState(false);

  const card: TokenCardProps = useMemo(
    () => ({
      style,
      tone,
      tier: tierName,
      value: `$${me.campaign.invitee_bonus_usd}`,
      caption: t("invite.cardCaption"),
      tagline: tagline.slice(0, 60),
      message: message.slice(0, 80),
      code: me.invite_code.toUpperCase(),
      redeemUrl: inviteUrl,
      serial: `REF · ${me.invite_code.toUpperCase()}`,
    }),
    [style, tone, tagline, message, tierName, me, t, inviteUrl],
  );

  const saveCard = async () => {
    setSaving(true);
    try {
      await apiPatch(`/referral/cards/${me.card.id}`, {
        card_style: style,
        card_tone: tone,
        tagline,
        message,
      });
      toast.success(t("invite.saved"));
      reload();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setSaving(false);
    }
  };

  const copyLink = async () => {
    await navigator.clipboard.writeText(inviteUrl);
    setCopied(true);
    toast.success(t("invite.copied"));
    setTimeout(() => setCopied(false), 1500);
  };

  const shareX = () => {
    const text = encodeURIComponent(t("invite.shareText"));
    window.open(
      `https://twitter.com/intent/tweet?text=${text}&url=${encodeURIComponent(inviteUrl)}`,
      "_blank",
      "noopener",
    );
  };

  const nativeShare = async () => {
    if (navigator.share) {
      await navigator
        .share({ title: "HypiToken", text: t("invite.shareText"), url: inviteUrl })
        .catch(() => {});
    } else {
      copyLink();
    }
  };

  return (
    <div className="grid gap-8 lg:grid-cols-[1.1fr_1fr]">
      <div className="space-y-4">
        <TokenCard {...card} />
        <div className="flex flex-wrap gap-2">
          {STYLE_OPTS.map((o) => (
            <button
              key={`${o.style}-${o.tone}`}
              type="button"
              onClick={() => {
                setStyle(o.style);
                setTone(o.tone);
              }}
              className={cn(
                "rounded-full border px-3 py-1.5 text-xs transition-colors",
                style === o.style && tone === o.tone
                  ? "border-primary bg-primary/10 text-primary"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              {o.label}
            </button>
          ))}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              tokenCardPNG(card, 2024).then((b) =>
                download(b, `hypitoken-invite-${me.invite_code}.png`),
              )
            }
          >
            <Download className="mr-1.5 size-4" /> PNG
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              download(
                new Blob([tokenCardSVG(card)], { type: "image/svg+xml" }),
                `hypitoken-invite-${me.invite_code}.svg`,
              )
            }
          >
            <Download className="mr-1.5 size-4" /> SVG
          </Button>
        </div>
      </div>

      <div className="space-y-5">
        <GlassPanel className="space-y-4 p-5">
          <div className="space-y-2">
            <Label>{t("invite.linkLabel")}</Label>
            <div className="flex gap-2">
              <Input readOnly value={inviteUrl} className="font-mono text-xs" />
              <Button variant="outline" size="icon" onClick={copyLink}>
                {copied ? <Check className="size-4 text-primary" /> : <Copy className="size-4" />}
              </Button>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button onClick={nativeShare} size="sm">
              <Share2 className="mr-1.5 size-4" /> {t("invite.share")}
            </Button>
            <Button onClick={shareX} variant="outline" size="sm">
              𝕏 / Twitter
            </Button>
            <Button onClick={copyLink} variant="outline" size="sm">
              <Link2 className="mr-1.5 size-4" /> {t("invite.copyLink")}
            </Button>
          </div>
        </GlassPanel>

        <GlassPanel className="space-y-4 p-5">
          <div className="space-y-2">
            <Label htmlFor="tagline">{t("invite.taglineLabel")}</Label>
            <Input
              id="tagline"
              value={tagline}
              maxLength={60}
              onChange={(e) => setTagline(e.target.value)}
              placeholder={t("invite.defaultTagline")}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="msg">{t("invite.messageLabel")}</Label>
            <Input
              id="msg"
              value={message}
              maxLength={80}
              onChange={(e) => setMessage(e.target.value)}
            />
          </div>
          <Button onClick={saveCard} disabled={saving} size="sm">
            {saving ? t("invite.saving") : t("invite.saveCard")}
          </Button>
        </GlassPanel>

        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("invite.fineprint", {
            invitee: fmtUSD(me.campaign.invitee_bonus_usd),
            inviter: fmtUSD(me.campaign.inviter_bonus_usd),
          })}
        </p>
      </div>
    </div>
  );
}

// ── Gift tab ────────────────────────────────────────────────────────────────

function GiftTab({ me, onSent }: { me: ReferralMe; onSent: () => void }) {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [amount, setAmount] = useState("5");
  const [message, setMessage] = useState("");
  const [style, setStyle] = useState<CardStyle>("claude");
  const [tone, setTone] = useState<CardTone>("dark");
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState<GiftCard[]>([]);

  const loadSent = () =>
    apiGet<{ gifts: GiftCard[] }>("/referral/gifts")
      .then((r) => setSent(r.gifts || []))
      .catch(() => {});
  // biome-ignore lint/correctness/useExhaustiveDependencies: run once on mount
  useEffect(() => {
    loadSent();
  }, []);

  const card: TokenCardProps = useMemo(
    () => ({
      style,
      tone,
      tier: "GIFT",
      value: `$${Number(amount) || 0}`,
      caption: t("gift.cardCaption"),
      tagline: t("gift.cardTagline"),
      message: message.slice(0, 80),
      code: "HYPI · GIFT",
      redeemUrl: absUrl(me.invite_url),
      serial: email ? `→ ${email}` : "",
    }),
    [style, tone, amount, message, email, me.invite_url, t],
  );

  const send = async () => {
    setSending(true);
    try {
      await apiPost<{ gift: GiftCard; balance_usd: number }>("/referral/gifts", {
        recipient_email: email.trim(),
        amount_usd: Number(amount),
        message,
        card_style: style,
        card_tone: tone,
      });
      toast.success(t("gift.sent"));
      setEmail("");
      setMessage("");
      loadSent();
      onSent();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="grid gap-8 lg:grid-cols-[1.1fr_1fr]">
      <div className="space-y-4">
        <TokenCard {...card} />
        <div className="flex flex-wrap gap-2">
          {STYLE_OPTS.map((o) => (
            <button
              key={`${o.style}-${o.tone}`}
              type="button"
              onClick={() => {
                setStyle(o.style);
                setTone(o.tone);
              }}
              className={cn(
                "rounded-full border px-3 py-1.5 text-xs transition-colors",
                style === o.style && tone === o.tone
                  ? "border-primary bg-primary/10 text-primary"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              {o.label}
            </button>
          ))}
        </div>
      </div>

      <div className="space-y-5">
        <GlassPanel className="space-y-4 p-5">
          {!me.can_send_gift && (
            // Stated before the form rather than on submit: composing a card and
            // only then being refused reads as a bug, not a rule.
            <div className="flex items-start gap-2.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2.5">
              <Lock className="mt-0.5 size-3.5 shrink-0 text-amber-500" />
              <div className="space-y-1.5 text-xs">
                <p className="text-foreground/90">{t("gift.locked")}</p>
                <Link to="/app/billing" className="inline-block text-amber-500 hover:underline">
                  {t("gift.lockedCta")}
                </Link>
              </div>
            </div>
          )}
          <div className="space-y-2">
            <Label htmlFor="gift-email">{t("gift.emailLabel")}</Label>
            <Input
              id="gift-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="friend@example.com"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="gift-amount">
              {t("gift.amountLabel", { max: fmtUSD(me.campaign.max_gift_usd) })}
            </Label>
            <Input
              id="gift-amount"
              type="number"
              min={1}
              max={me.campaign.max_gift_usd}
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="gift-msg">{t("gift.messageLabel")}</Label>
            <Textarea
              id="gift-msg"
              value={message}
              maxLength={140}
              rows={2}
              onChange={(e) => setMessage(e.target.value)}
            />
          </div>
          <Button
            onClick={send}
            disabled={sending || !email || Number(amount) <= 0 || !me.can_send_gift}
          >
            <Send className="mr-1.5 size-4" /> {sending ? t("gift.sending") : t("gift.sendBtn")}
          </Button>
          <p className="text-xs text-muted-foreground">
            {t("gift.note", { days: me.campaign.gift_expiry_days })}
          </p>
        </GlassPanel>

        {sent.length > 0 && (
          <GlassPanel className="p-5">
            <h3 className="mb-3 text-sm font-medium">{t("gift.sentTitle")}</h3>
            <div className="space-y-2">
              {sent.map((g) => (
                <GiftRow key={g.id} g={g} />
              ))}
            </div>
          </GlassPanel>
        )}
      </div>
    </div>
  );
}

// ── Received tab ────────────────────────────────────────────────────────────

function ReceivedTab({ initialCode, onClaim }: { initialCode: string; onClaim: () => void }) {
  const { t } = useTranslation();
  const [code, setCode] = useState(initialCode);
  const [claiming, setClaiming] = useState(false);
  const [received, setReceived] = useState<GiftCard[]>([]);

  const load = () =>
    apiGet<{ gifts: GiftCard[] }>("/referral/gifts/received")
      .then((r) => setReceived(r.gifts || []))
      .catch(() => {});
  // biome-ignore lint/correctness/useExhaustiveDependencies: run once on mount
  useEffect(() => {
    load();
  }, []);

  const claim = async () => {
    if (!code.trim()) return;
    setClaiming(true);
    try {
      const r = await apiPost<{ gift: GiftCard; balance_usd: number }>("/referral/gifts/claim", {
        code,
      });
      toast.success(t("gift.claimed", { amount: fmtUSD(r.gift.amount_usd) }));
      setCode("");
      load();
      onClaim();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setClaiming(false);
    }
  };

  return (
    <div className="grid gap-8 lg:grid-cols-2">
      <GlassPanel className="space-y-4 p-5">
        <div className="space-y-2">
          <Label htmlFor="claim-code">{t("gift.claimLabel")}</Label>
          <div className="flex gap-2">
            <Input
              id="claim-code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="HYPI-XXXX-XXXX"
              className="font-mono uppercase"
            />
            <Button onClick={claim} disabled={claiming || !code.trim()}>
              {claiming ? t("gift.claiming") : t("gift.claimBtn")}
            </Button>
          </div>
        </div>
      </GlassPanel>

      <GlassPanel className="p-5">
        <h3 className="mb-3 text-sm font-medium">{t("gift.receivedTitle")}</h3>
        {received.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("gift.noneReceived")}</p>
        ) : (
          <div className="space-y-2">
            {received.map((g) => (
              <GiftRow key={g.id} g={g} />
            ))}
          </div>
        )}
      </GlassPanel>
    </div>
  );
}

function GiftRow({ g }: { g: GiftCard }) {
  const { t } = useTranslation();
  const statusColor: Record<string, string> = {
    pending: "text-amber-500",
    claimed: "text-emerald-500",
    expired: "text-muted-foreground",
    refunded: "text-sky-500",
  };
  return (
    <div className="flex items-center justify-between rounded-lg border border-border/60 px-3 py-2 text-sm">
      <div className="flex flex-col">
        <span className="font-mono text-xs text-muted-foreground">{g.code}</span>
        <span className="text-xs text-muted-foreground">{g.recipient_email}</span>
      </div>
      <div className="flex items-center gap-3">
        <span className="font-medium">{fmtUSD(g.amount_usd)}</span>
        <span className={cn("text-xs", statusColor[g.status])}>{t(`gift.status.${g.status}`)}</span>
      </div>
    </div>
  );
}
