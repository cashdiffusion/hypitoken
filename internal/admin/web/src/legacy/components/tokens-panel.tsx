import { useMemo, useState } from "react";
import { Plus, Search, X } from "lucide-react";
import type { ClientRow, Summary } from "@/legacy/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { GroupBadge } from "./group-badge";
import { CopyTokenBtn } from "./copy-token-btn";
import { cn, fmtDate, fmtInt, isoWeekRange } from "@/legacy/lib/utils";

interface Props {
  summary: Summary | null;
  onAdd: () => void;
  onEdit: (cl: ClientRow) => void;
  onDelete: (cl: ClientRow) => void;
}

type SortKey = "name" | "weekly" | "total" | "last";
type SortDir = "asc" | "desc";

export function TokensPanel({ summary, onAdd, onEdit, onDelete }: Props) {
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; dir: SortDir }>({ key: "weekly", dir: "desc" });
  const [filter, setFilter] = useState<"all" | "managed" | "config" | "blocked">("all");

  const clients = summary?.clients || [];
  const totalWeekly = clients.reduce((s, c) => s + c.weekly_usd, 0);
  const blockedCount = clients.filter((c) => c.blocked).length;
  const managedCount = clients.filter((c) => c.managed).length;

  const filtered = useMemo(() => {
    const ql = q.trim().toLowerCase();
    return clients.filter((c) => {
      if (filter === "managed" && !c.managed) return false;
      if (filter === "config" && !c.from_config) return false;
      if (filter === "blocked" && !c.blocked) return false;
      if (!ql) return true;
      return (
        c.token.toLowerCase().includes(ql) ||
        (c.label || "").toLowerCase().includes(ql) ||
        (c.group || "").toLowerCase().includes(ql)
      );
    });
  }, [clients, q, filter]);

  const sorted = useMemo(() => {
    const arr = [...filtered];
    arr.sort((a, b) => {
      const dir = sort.dir === "asc" ? 1 : -1;
      switch (sort.key) {
        case "name":
          return dir * (a.label || "").localeCompare(b.label || "");
        case "weekly":
          return dir * (a.weekly_usd - b.weekly_usd);
        case "total":
          return dir * (a.total.cost_usd - b.total.cost_usd);
        case "last": {
          const at = a.last_used ? new Date(a.last_used).getTime() : 0;
          const bt = b.last_used ? new Date(b.last_used).getTime() : 0;
          return dir * (at - bt);
        }
      }
    });
    return arr;
  }, [filtered, sort]);

  const toggleSort = (key: SortKey) => {
    setSort((s) => (s.key === key ? { key, dir: s.dir === "asc" ? "desc" : "asc" } : { key, dir: "desc" }));
  };
  const sortHead = (key: SortKey, label: string, align: "left" | "right" = "left") => (
    <button
      type="button"
      onClick={() => toggleSort(key)}
      className={cn(
        "inline-flex items-center gap-1 hover:text-foreground transition-colors",
        align === "right" && "ml-auto",
      )}
    >
      <span>{label}</span>
      {sort.key === key && (
        <span className="text-[8px] opacity-70">{sort.dir === "asc" ? "▲" : "▼"}</span>
      )}
    </button>
  );

  const filterPills: { key: typeof filter; label: string; count: number }[] = [
    { key: "all", label: "All", count: clients.length },
    { key: "managed", label: "Managed", count: managedCount },
    { key: "config", label: "Config", count: clients.length - managedCount },
    { key: "blocked", label: "Blocked", count: blockedCount },
  ];

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <div className="eyebrow mb-1.5">§ Client tokens</div>
          <h2 className="font-display text-3xl md:text-4xl tracking-tight flex items-baseline flex-wrap gap-3">
            <span>
              Billing week{" "}
              <span className="text-muted-foreground">{summary?.current_week || "···"}</span>
            </span>
            {summary?.current_week && (
              <span className="eyebrow opacity-70">{isoWeekRange(summary.current_week)} UTC</span>
            )}
          </h2>
          <p className="text-sm text-muted-foreground mt-1.5 mono tabular">
            {fmtInt(clients.length)} token(s) · weekly spend{" "}
            <span className="font-medium text-foreground">${totalWeekly.toFixed(4)}</span>
            {blockedCount > 0 && (
              <>
                {" "}
                · <span className="text-destructive">{blockedCount} blocked</span>
              </>
            )}
          </p>
        </div>
        <Button onClick={onAdd} className="gap-2">
          <Plus className="h-4 w-4" />
          New token
        </Button>
      </header>

      <div className="flex flex-col md:flex-row md:items-center gap-3">
        <div className="relative flex-1 max-w-md">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
          <Input
            placeholder="Search by name, token, or group…"
            value={q}
            onChange={(e) => setQ(e.currentTarget.value)}
            className="pl-9 pr-9 font-mono text-xs"
          />
          {q && (
            <button
              type="button"
              onClick={() => setQ("")}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              aria-label="Clear"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <div className="flex items-center gap-1 p-1 bg-muted/60 border border-border rounded-md w-fit">
          {filterPills.map(({ key, label, count }) => {
            const active = filter === key;
            return (
              <button
                key={key}
                onClick={() => setFilter(key)}
                className={cn(
                  "px-3 py-1.5 rounded-sm text-xs font-medium flex items-baseline gap-1.5 transition-all",
                  active
                    ? "bg-card text-foreground shadow-sm border border-border"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                <span>{label}</span>
                <span className="mono tabular opacity-60 text-[10px]">{count}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="bg-card border border-border-strong rounded-md overflow-hidden">
        {!summary ? (
          <div className="py-16 text-center eyebrow animate-pulse">
            <span className="opacity-60">Loading tokens…</span>
          </div>
        ) : sorted.length === 0 ? (
          <div className="py-14 px-6 text-center text-sm text-muted-foreground font-mono">
            {q || filter !== "all"
              ? "No tokens match the current filter."
              : "No client tokens yet — click New token above, or the proxy is running in open mode."}
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left border-b border-border-strong bg-muted/30">
                <tr className="eyebrow">
                  <th className="py-3 px-4 font-[inherit]">{sortHead("name", "Name")}</th>
                  <th className="py-3 px-4 font-[inherit]">Token</th>
                  <th className="py-3 px-4 font-[inherit]">Group</th>
                  <th className="py-3 px-4 font-[inherit]">{sortHead("weekly", "Weekly spend")}</th>
                  <th className="py-3 px-4 font-[inherit]">Limit</th>
                  <th
                    className="py-3 px-4 font-[inherit] cursor-help"
                    title="Lifetime cumulative spend per client — persisted in usage state, not derived from request logs."
                  >
                    {sortHead("total", "Total")}
                  </th>
                  <th className="py-3 px-4 font-[inherit]">{sortHead("last", "Last used")}</th>
                  <th className="py-3 px-4 font-[inherit] text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((cl) => {
                  const ratio = cl.weekly_limit > 0 ? cl.weekly_usd / cl.weekly_limit : 0;
                  return (
                    <tr
                      key={cl.token}
                      className={cn(
                        "border-b border-border last:border-0 hover:bg-muted/50 transition-colors",
                        cl.blocked && "bg-destructive/5",
                      )}
                    >
                      <td className="py-3 px-4">
                        <div className="font-medium flex items-center gap-2">
                          {cl.blocked && (
                            <span
                              className="inline-block h-1.5 w-1.5 rounded-full bg-destructive"
                              title="Blocked"
                            />
                          )}
                          {cl.label || (
                            <span className="text-muted-foreground">(unnamed)</span>
                          )}
                        </div>
                        {cl.from_config && (
                          <div className="mono text-[10px] uppercase tracking-wider text-muted-foreground mt-0.5">
                            from config · read-only
                          </div>
                        )}
                      </td>
                      <td className="py-3 px-4 mono text-xs text-muted-foreground">{cl.token}</td>
                      <td className="py-3 px-4">
                        <GroupBadge group={cl.group} />
                      </td>
                      <td className="py-3 px-4 mono text-sm">
                        <div
                          className="font-medium"
                          style={{
                            color: cl.blocked
                              ? "var(--destructive)"
                              : ratio > 0.8
                                ? "var(--warning)"
                                : undefined,
                          }}
                        >
                          ${cl.weekly_usd.toFixed(4)}
                        </div>
                        {cl.weekly_limit > 0 && (
                          <div className="mt-1.5 h-1 w-28 bg-muted rounded-full overflow-hidden">
                            <div
                              className="h-full transition-all"
                              style={{
                                width: `${Math.min(100, Math.round(ratio * 100))}%`,
                                background: cl.blocked
                                  ? "var(--destructive)"
                                  : ratio > 0.8
                                    ? "var(--warning)"
                                    : "var(--success)",
                              }}
                            />
                          </div>
                        )}
                      </td>
                      <td className="py-3 px-4 mono text-sm">
                        {cl.weekly_limit > 0 ? (
                          "$" + cl.weekly_limit.toFixed(2)
                        ) : (
                          <span className="text-muted-foreground">none</span>
                        )}
                      </td>
                      <td className="py-3 px-4 mono text-sm">
                        <div>${cl.total.cost_usd.toFixed(4)}</div>
                        <div className="text-[10px] uppercase tracking-wider text-muted-foreground mt-0.5">
                          {fmtInt(cl.total.requests)} req
                        </div>
                      </td>
                      <td className="py-3 px-4 text-xs mono">
                        {cl.last_used ? (
                          fmtDate(cl.last_used)
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                      <td className="py-3 px-4 text-right">
                        {cl.full_token ? (
                          <div className="flex gap-1.5 justify-end flex-wrap">
                            <CopyTokenBtn token={cl.full_token} />
                            <Button size="sm" variant="outline" onClick={() => onEdit(cl)}>
                              Edit
                            </Button>
                            {cl.managed && (
                              <Button
                                size="sm"
                                variant="outline"
                                className="border-destructive/40 text-destructive hover:bg-destructive/10"
                                onClick={() => onDelete(cl)}
                              >
                                Delete
                              </Button>
                            )}
                          </div>
                        ) : (
                          <span className="text-xs text-muted-foreground">—</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
