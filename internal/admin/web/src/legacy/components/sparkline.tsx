import type { DayEntry } from "@/legacy/lib/types";
import { cn } from "@/legacy/lib/utils";

export function Sparkline({ daily }: { daily: DayEntry[] }) {
  if (!daily.length) return null;
  const map = Object.fromEntries(daily.map((d) => [d.date, d.counts]));
  const end = daily[daily.length - 1]!.date;
  const days = 14;
  const out: { date: string; val: number }[] = [];
  const endD = new Date(end + "T00:00:00Z");
  for (let i = days - 1; i >= 0; i--) {
    const d = new Date(endD);
    d.setUTCDate(endD.getUTCDate() - i);
    const key = d.toISOString().slice(0, 10);
    const c = map[key] || { input_tokens: 0, output_tokens: 0 };
    out.push({
      date: key,
      val: (c.input_tokens || 0) + (c.output_tokens || 0),
    });
  }
  const max = Math.max(1, ...out.map((o) => o.val));
  return (
    <div className="flex items-end gap-[2px] h-10 w-[88px]">
      {out.map((o) => {
        const pct = Math.round((o.val / max) * 100);
        return (
          <div
            key={o.date}
            title={`${o.date}: ${o.val.toLocaleString()} tokens`}
            className={cn(
              "w-[4px] rounded-sm",
              o.val > 0 ? "bg-slate-700 dark:bg-slate-300" : "bg-slate-200 dark:bg-slate-600",
            )}
            style={{ height: `${Math.max(pct, o.val > 0 ? 6 : 2)}%` }}
          />
        );
      })}
    </div>
  );
}
