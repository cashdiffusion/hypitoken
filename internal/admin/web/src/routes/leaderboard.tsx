import { Activity, Hash, Trophy, Users } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { CountUp, GlassPanel, PageHeader, StatTile } from "@/components/app/page-primitives";
import {
  type OfficePlayer,
  PixelOffice,
  type PixelOfficeHandle,
} from "@/components/arena/pixel-office";
import { Reveal, RevealItem, RevealStagger } from "@/components/landing/reveal";
import { useAuth } from "@/hooks/use-auth";
import { apiGet, getJWT } from "@/lib/api";
import type { ArenaEvent, LeaderboardResponse, LeaderRow } from "@/lib/types";
import { cn } from "@/lib/utils";

type Metric = "tokens" | "requests";

export default function LeaderboardPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const [metric, setMetric] = useState<Metric>("tokens");
  const [data, setData] = useState<LeaderboardResponse | null>(null);
  const officeRef = useRef<PixelOfficeHandle | null>(null);

  // Fetch (and refetch on metric change). Also poll lightly so counters that
  // moved while we were watching the office settle to authoritative values.
  useEffect(() => {
    let alive = true;
    const load = () =>
      apiGet<LeaderboardResponse>(`/arena/leaderboard?metric=${metric}`)
        .then((r) => {
          if (alive) setData(r);
        })
        .catch(() => {});
    load();
    const id = window.setInterval(load, 30_000);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, [metric]);

  // Single SSE connection for the whole page. EventSource can't set headers, so
  // the JWT rides the query string (the stream endpoint accepts ?access_token).
  useEffect(() => {
    const jwt = getJWT();
    if (!jwt) return;
    const es = new EventSource(`/api/v2/arena/stream?access_token=${encodeURIComponent(jwt)}`);
    es.onmessage = (ev) => {
      let e: ArenaEvent;
      try {
        e = JSON.parse(ev.data) as ArenaEvent;
      } catch {
        return;
      }
      if (!e.actor) return;
      officeRef.current?.pulse(e.actor);
      // Optimistically nudge the matching row so the board feels live between
      // the 30s authoritative refetches.
      setData((prev) => {
        if (!prev) return prev;
        const bump = (r: LeaderRow): LeaderRow =>
          r.actor === e.actor
            ? {
                ...r,
                tokens: r.tokens + e.tokens,
                requests: r.requests + 1,
                last_seen: e.ts / 1000,
              }
            : r;
        return {
          ...prev,
          rows: prev.rows.map(bump),
          you: prev.you ? bump(prev.you) : prev.you,
        };
      });
    };
    return () => es.close();
  }, []);

  const rows = data?.rows ?? [];
  const you = data?.you ?? null;
  // Your own character always shows YOUR nickname on YOUR view — the public
  // opt-in governs what *others* see of you, not what you see of yourself, so
  // even an anonymous (opted-out) user can spot themselves by name.
  const myName = user?.display_name?.trim();
  const players: OfficePlayer[] = rows.map((r) => ({
    actor: r.actor,
    name: r.is_you && myName ? myName : r.name,
    isYou: r.is_you,
  }));

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow={t("nav.arena")}
        title={t("arena.title")}
        sub={t("arena.subtitle")}
        icon={Trophy}
        action={<MetricToggle metric={metric} onChange={setMetric} />}
      />

      {/* Your standing */}
      <RevealStagger className="grid gap-4 md:grid-cols-3">
        <RevealItem className="flex">
          <StatTile
            icon={Trophy}
            label={t("arena.yourRank")}
            value={
              you && you.rank > 0 ? <span>#{you.rank}</span> : <span>{t("arena.notRanked")}</span>
            }
            accent
            sub={you?.public ? t("arena.publicNote") : t("arena.anonymousNote")}
          />
        </RevealItem>
        <RevealItem className="flex">
          <StatTile
            icon={Hash}
            label={t("arena.metricTokens")}
            value={<CountUp value={you?.tokens ?? 0} format={(n) => compact(n)} />}
          />
        </RevealItem>
        <RevealItem className="flex">
          <StatTile
            icon={Activity}
            label={t("arena.metricRequests")}
            value={<CountUp value={you?.requests ?? 0} format={(n) => compact(n)} />}
          />
        </RevealItem>
      </RevealStagger>

      {/* Pixel office */}
      <Reveal>
        <GlassPanel
          title={
            <span className="flex items-center gap-2">
              <Users className="h-4.5 w-4.5 text-primary" />
              {t("arena.office")}
              <LiveDot />
            </span>
          }
          description={t("arena.officeHint")}
        >
          {players.length === 0 ? (
            <EmptyOffice text={t("arena.empty")} />
          ) : (
            <div className="overflow-hidden rounded-xl border border-border/50 bg-[#101a14]">
              <PixelOffice ref={officeRef} players={players} />
            </div>
          )}
        </GlassPanel>
      </Reveal>

      {/* Leaderboard table */}
      <Reveal>
        <GlassPanel title={t("arena.title")} bodyClassName="p-0">
          {rows.length === 0 ? (
            <div className="m-5 rounded-xl border border-dashed border-border-strong p-8 text-center text-sm text-muted-foreground md:m-6">
              {t("arena.empty")}
            </div>
          ) : (
            <div className="divide-y divide-border/60">
              {rows.map((r) => (
                <LeaderRowItem key={r.actor} row={r} metric={metric} myName={myName} />
              ))}
            </div>
          )}
        </GlassPanel>
      </Reveal>
    </div>
  );
}

function MetricToggle({ metric, onChange }: { metric: Metric; onChange: (m: Metric) => void }) {
  const { t } = useTranslation();
  const opts: { key: Metric; label: string }[] = [
    { key: "tokens", label: t("arena.metricTokens") },
    { key: "requests", label: t("arena.metricRequests") },
  ];
  return (
    <div className="glass inline-flex rounded-lg p-1">
      {opts.map((o) => (
        <button
          key={o.key}
          type="button"
          onClick={() => onChange(o.key)}
          className={cn(
            "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
            metric === o.key
              ? "bg-primary/15 text-primary"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

function LeaderRowItem({
  row,
  metric,
  myName,
}: {
  row: LeaderRow;
  metric: Metric;
  myName?: string;
}) {
  const { t } = useTranslation();
  const value = metric === "tokens" ? row.tokens : row.requests;
  return (
    <div
      className={cn(
        "flex items-center gap-3 px-5 py-3 text-sm transition-colors hover:bg-primary/[0.03] md:px-6",
        row.is_you && "bg-primary/[0.06]",
      )}
    >
      <span className="w-8 shrink-0 text-center">
        <Medal rank={row.rank} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className={cn("truncate font-medium", row.is_you && "text-primary")}>
            {row.is_you && myName ? myName : row.name}
          </span>
          {row.is_you && (
            <span className="rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary">
              {t("arena.you")}
            </span>
          )}
          {!row.public && (
            <span className="rounded-full bg-muted/60 px-1.5 py-0.5 text-[10px] text-muted-foreground">
              {t("arena.anonymousNote")}
            </span>
          )}
        </div>
        <div className="text-xs text-muted-foreground">
          {t("arena.lastSeen")}: {relTime(row.last_seen, t)}
        </div>
      </div>
      <div className="shrink-0 text-right">
        <div className="font-mono font-semibold tabular-nums">{compact(value)}</div>
        <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
          {metric === "tokens" ? t("arena.tokens") : t("arena.requests")}
        </div>
      </div>
    </div>
  );
}

function Medal({ rank }: { rank: number }) {
  if (rank === 1) return <span className="text-lg">🥇</span>;
  if (rank === 2) return <span className="text-lg">🥈</span>;
  if (rank === 3) return <span className="text-lg">🥉</span>;
  return <span className="font-mono text-xs text-muted-foreground">{rank}</span>;
}

function LiveDot() {
  return (
    <span className="inline-flex items-center gap-1 text-[10px] font-medium uppercase tracking-wider text-primary">
      <span className="relative flex h-2 w-2">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary/60" />
        <span className="relative inline-flex h-2 w-2 rounded-full bg-primary" />
      </span>
    </span>
  );
}

function EmptyOffice({ text }: { text: string }) {
  return (
    <div className="grid place-items-center rounded-xl border border-dashed border-border-strong bg-[#101a14] p-12 text-center text-sm text-muted-foreground">
      {text}
    </div>
  );
}

// compact renders large counts as 1.2k / 3.4M.
function compact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(Math.round(n));
}

// relTime renders a unix-seconds timestamp as a localized relative string.
function relTime(sec: number, t: (k: string, o?: Record<string, unknown>) => string): string {
  if (!sec) return "—";
  const diff = Date.now() / 1000 - sec;
  if (diff < 60) return t("arena.justNow");
  if (diff < 3600) return t("arena.minutesAgo", { n: Math.floor(diff / 60) });
  if (diff < 86400) return t("arena.hoursAgo", { n: Math.floor(diff / 3600) });
  return t("arena.daysAgo", { n: Math.floor(diff / 86400) });
}
