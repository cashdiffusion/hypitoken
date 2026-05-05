import { cn } from "@/lib/utils";

export interface SparkPoint {
  label: string;
  value: number;
}

// Compact 24-bar sparkline used in the operator overview. Heights are
// proportional to the bucket value, with a 2px floor so empty buckets stay
// visible. Title attribute carries the per-bar tooltip.
export function Sparkline({ data, className }: { data: SparkPoint[]; className?: string }) {
  if (!data.length) return null;
  const max = Math.max(1, ...data.map((d) => d.value));
  return (
    <div className={cn("flex items-end gap-[2px] h-12", className)}>
      {data.map((d, i) => {
        const pct = Math.round((d.value / max) * 100);
        return (
          <div
            key={i}
            title={`${d.label}: ${d.value.toLocaleString()}`}
            className={cn(
              "flex-1 min-w-[3px] rounded-sm",
              d.value > 0 ? "bg-foreground/80" : "bg-muted",
            )}
            style={{ height: `${Math.max(pct, d.value > 0 ? 6 : 2)}%` }}
          />
        );
      })}
    </div>
  );
}
