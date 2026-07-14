import { Activity, ChartColumnBig, DollarSign, Flame, KeyRound } from "lucide-react";
import { motion } from "motion/react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { CountUp, GlassPanel, PageHeader, StatTile } from "@/components/app/page-primitives";
import { Pager } from "@/components/app/pager";
import { SpendHeatmap } from "@/components/app/spend-heatmap";
import { StreakFlame } from "@/components/app/streak-flame";
import { ModelBreakdown, SpendTrend, TokenComparison } from "@/components/app/usage-charts";
import {
  defaultFilter,
  ExportButton,
  FilterBar,
  type UsageFilter,
  usageQuery,
} from "@/components/app/usage-filters";
import { Reveal, RevealItem, RevealStagger } from "@/components/landing/reveal";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useAuth } from "@/hooks/use-auth";
import { apiGet } from "@/lib/api";
import type { SpendRow, UsageSummary } from "@/lib/types";
import { cn, errMsg, fmtCompact, fmtUSD } from "@/lib/utils";

/* /app/usage — where the money went.
 *
 * One route serves two scopes: the signed-in user's own spend, and (for an
 * enterprise space admin) their whole team's. The charts, filters and export are
 * identical — only the endpoint prefix and one extra column differ — so a scope
 * is a `?ws=<id>` param rather than a second page.
 *
 * This is deliberately NOT merged into /app/billing (a wallet: balance, top-ups,
 * orders) or /app/console (throughput: requests and tokens, sourced from the
 * request log). This page is about money already spent, sourced from the ledger.
 */

const ROWS_PER_PAGE = 25;

export default function UsagePage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const [params, setParams] = useSearchParams();

  // Spaces this user administers — the only ones whose team view they can open.
  const adminSpaces = useMemo(
    () => (user?.workspaces || []).filter((w) => w.type === "enterprise" && w.role === "admin"),
    [user],
  );

  const wsParam = params.get("ws");
  const scopeWs =
    wsParam && adminSpaces.some((w) => String(w.id) === wsParam) ? Number(wsParam) : null;
  const basePath = scopeWs ? `/workspaces/${scopeWs}/usage` : "/me/usage";
  const showMember = scopeWs != null;

  const [filter, setFilter] = useState<UsageFilter>(defaultFilter);
  const [summary, setSummary] = useState<UsageSummary | null>(null);
  const [universe, setUniverse] = useState<UsageSummary | null>(null);
  const [rows, setRows] = useState<SpendRow[]>([]);
  const [rowTotal, setRowTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const query = usageQuery(filter);
  // The filter controls (token / model / tag) and the token-comparison chart
  // need the FULL set for the current date range — never the token/model/tag-
  // narrowed set the main summary returns. Fetching summary filtered by token X
  // makes by_token collapse to just X, which is what left the picker with only
  // "all" + the one already chosen. So drive them from an unfiltered-by-facet
  // fetch keyed on date + scope only, so picking a token doesn't refetch it.
  const universeQuery = new URLSearchParams({ from: filter.from, to: filter.to }).toString();

  // Summary (charts + heatmap + streak). Debounced so dragging a date input
  // doesn't fire a request per keystroke.
  useEffect(() => {
    let cancelled = false;
    const timer = setTimeout(() => {
      setLoading(true);
      apiGet<UsageSummary>(`${basePath}/summary?${query}`)
        .then((d) => {
          if (cancelled) return;
          setSummary(d);
          setError("");
        })
        .catch((e) => !cancelled && setError(errMsg(e)))
        .finally(() => !cancelled && setLoading(false));
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [basePath, query]);

  // Token/model/tag universe for the current range + scope. Populates the
  // filter dropdowns and the comparison chart with every key, independent of
  // which one is currently selected.
  useEffect(() => {
    let cancelled = false;
    const timer = setTimeout(() => {
      apiGet<UsageSummary>(`${basePath}/summary?${universeQuery}`)
        .then((d) => !cancelled && setUniverse(d))
        .catch(() => {});
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [basePath, universeQuery]);

  // Detail rows follow the same filter, plus pagination.
  useEffect(() => {
    let cancelled = false;
    const timer = setTimeout(() => {
      apiGet<{ rows: SpendRow[]; total: number }>(
        `${basePath}/rows?${query}&limit=${ROWS_PER_PAGE}&offset=${offset}`,
      )
        .then((d) => {
          if (cancelled) return;
          setRows(d.rows || []);
          setRowTotal(d.total || 0);
        })
        .catch(() => {});
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [basePath, query, offset]);

  // Any filter change invalidates the current page of rows.
  const changeFilter = useCallback((f: UsageFilter) => {
    setFilter(f);
    setOffset(0);
  }, []);

  const switchScope = (ws: number | null) => {
    setParams(ws ? { ws: String(ws) } : {}, { replace: true });
    setOffset(0);
  };

  const total = summary?.total;
  const avgCost = total && total.charge_events > 0 ? total.spent_usd / total.charge_events : 0;

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow={t("nav.usage")}
        icon={ChartColumnBig}
        title={scopeWs ? t("usage.teamTitle") : t("usage.title")}
        sub={scopeWs ? t("usage.teamSub") : t("usage.sub")}
        action={<ExportButton basePath={basePath} filter={filter} />}
      />

      {/* scope switch — only an enterprise space admin ever sees a second pill */}
      {adminSpaces.length > 0 && (
        <Reveal>
          <div className="inline-flex items-center gap-0.5 rounded-full bg-muted/60 p-0.5">
            <ScopePill
              active={!scopeWs}
              onClick={() => switchScope(null)}
              label={t("usage.scopePersonal")}
            />
            {adminSpaces.map((w) => (
              <ScopePill
                key={w.id}
                active={scopeWs === w.id}
                onClick={() => switchScope(w.id)}
                label={w.name}
              />
            ))}
          </div>
        </Reveal>
      )}

      <Reveal>
        <FilterBar
          value={filter}
          onChange={changeFilter}
          tokens={universe?.by_token || []}
          models={universe?.by_model || []}
          tags={universe?.by_tag || []}
          showMember={showMember}
        />
      </Reveal>

      {error && (
        <div className="rounded-xl border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      )}

      {summary && (
        <Reveal>
          <StreakFlame
            current={summary.streak.current_days}
            longest={summary.streak.longest_days}
          />
        </Reveal>
      )}

      <RevealStagger className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <RevealItem>
          <StatTile
            icon={DollarSign}
            accent
            label={t("usage.stat.spent")}
            value={<CountUp value={total?.spent_usd ?? 0} format={fmtUSD} />}
            sub={t("usage.stat.spentSub", {
              from: summary?.range.from ?? "",
              to: summary?.range.to ?? "",
            })}
          />
        </RevealItem>
        <RevealItem>
          <StatTile
            icon={Activity}
            label={t("usage.stat.events")}
            value={
              <CountUp
                value={total?.charge_events ?? 0}
                format={(n) => fmtCompact(Math.round(n))}
              />
            }
            /* "计费笔数", never "请求数": one call can bill several times (advisor
               sub-models) and a zero-cost call bills not at all. */
            sub={t("usage.stat.eventsSub", { avg: fmtUSD(avgCost) })}
          />
        </RevealItem>
        <RevealItem>
          <StatTile
            icon={KeyRound}
            label={t("usage.stat.activeTokens")}
            value={
              <CountUp value={total?.active_tokens ?? 0} format={(n) => String(Math.round(n))} />
            }
            sub={t("usage.stat.activeTokensSub")}
          />
        </RevealItem>
        <RevealItem>
          <StatTile
            icon={Flame}
            label={t("usage.stat.streak")}
            value={
              <CountUp
                value={summary?.streak.current_days ?? 0}
                format={(n) => String(Math.round(n))}
              />
            }
            sub={t("usage.streak.longest", { n: summary?.streak.longest_days ?? 0 })}
          />
        </RevealItem>
      </RevealStagger>

      <Reveal>
        <GlassPanel title={t("usage.heatmap.title")} description={t("usage.heatmap.sub")}>
          <SpendHeatmap
            days={summary?.by_day || []}
            selectedDay={filter.from === filter.to ? filter.from : undefined}
            onSelectDay={(day) => changeFilter({ ...filter, range: "custom", from: day, to: day })}
          />
        </GlassPanel>
      </Reveal>

      <Reveal>
        <GlassPanel title={t("usage.chart.trend")} description={t("usage.chart.trendSub")}>
          <SpendTrend days={summary?.by_day || []} />
        </GlassPanel>
      </Reveal>

      <Reveal>
        <GlassPanel title={t("usage.chart.byToken")} description={t("usage.chart.byTokenSub")}>
          <TokenComparison
            tokens={universe?.by_token || []}
            selectedTokenId={filter.tokenId}
            showMember={showMember}
            onSelectToken={(id) =>
              changeFilter({ ...filter, tokenId: filter.tokenId === id ? undefined : id })
            }
          />
        </GlassPanel>
      </Reveal>

      <Reveal>
        <GlassPanel title={t("usage.chart.byModel")} description={t("usage.chart.byModelSub")}>
          <ModelBreakdown models={summary?.by_model || []} />
        </GlassPanel>
      </Reveal>

      <Reveal>
        <GlassPanel
          title={t("usage.table.title")}
          description={t("usage.table.sub")}
          bodyClassName="p-0"
        >
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("usage.table.colTime")}</TableHead>
                  {showMember && <TableHead>{t("usage.table.colMember")}</TableHead>}
                  <TableHead>{t("usage.table.colToken")}</TableHead>
                  <TableHead>{t("usage.table.colModel")}</TableHead>
                  <TableHead className="text-right">{t("usage.table.colTokens")}</TableHead>
                  <TableHead className="text-right">{t("usage.table.colSpend")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.length === 0 && (
                  <TableRow>
                    <TableCell
                      colSpan={showMember ? 6 : 5}
                      className="py-10 text-center text-sm text-muted-foreground"
                    >
                      {loading ? t("common.loading") : t("usage.table.empty")}
                    </TableCell>
                  </TableRow>
                )}
                {rows.map((r) => (
                  <TableRow key={r.id} className={cn(!r.attributed && "opacity-60")}>
                    <TableCell className="whitespace-nowrap font-mono text-xs">
                      {new Date(r.created_at * 1000).toLocaleString()}
                    </TableCell>
                    {showMember && (
                      <TableCell className="text-sm text-muted-foreground">{r.email}</TableCell>
                    )}
                    <TableCell>
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="text-sm">
                          {r.token_name ||
                            (r.token_id === 0
                              ? t("usage.token.unattributed")
                              : t("usage.token.deleted", { id: r.token_id }))}
                        </span>
                        {r.token_tags.map((tag) => (
                          <Badge key={tag} variant="outline" className="text-[10px]">
                            {tag}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{r.model || "—"}</TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums text-muted-foreground">
                      {/* Blank, not 0: a pre-v15 row's token counts are unknown. */}
                      {r.attributed
                        ? fmtCompact(
                            r.input_tokens +
                              r.output_tokens +
                              r.cache_read_tokens +
                              r.cache_create_tokens,
                          )
                        : "—"}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {fmtUSD(r.amount_usd)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="border-t border-border/60 px-5 py-3 md:px-6">
            <Pager
              offset={offset}
              limit={ROWS_PER_PAGE}
              total={rowTotal}
              count={rows.length}
              onChange={setOffset}
            />
          </div>
        </GlassPanel>
      </Reveal>
    </div>
  );
}

function ScopePill({
  active,
  onClick,
  label,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "relative rounded-full px-4 py-1.5 text-sm font-medium transition-colors",
        active ? "text-primary-foreground" : "text-muted-foreground hover:text-foreground",
      )}
    >
      {active && (
        <motion.span
          layoutId="usage-scope-pill"
          className="absolute inset-0 rounded-full bg-primary"
          transition={{ type: "spring", stiffness: 380, damping: 30 }}
        />
      )}
      <span className="relative z-10">{label}</span>
    </button>
  );
}
