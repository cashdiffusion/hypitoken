import * as React from "react";
import * as RechartsPrimitive from "recharts";
import { cn } from "@/lib/utils";

// Minimal shadcn-style Chart primitive. Consumers pass a ChartConfig that
// maps data keys to labels + colors; the container exposes those colors
// as --color-<key> CSS vars so children can reference them via
// stroke="var(--color-key)".

export type ChartConfig = {
  [k in string]: {
    label?: React.ReactNode;
    color?: string;
    theme?: Record<"light" | "dark", string>;
  };
};

type ChartContextProps = { config: ChartConfig };
const ChartContext = React.createContext<ChartContextProps | null>(null);
function useChart() {
  const ctx = React.useContext(ChartContext);
  if (!ctx) throw new Error("useChart must be used inside <ChartContainer />");
  return ctx;
}

function buildCSSVars(config: ChartConfig, isDark: boolean): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, c] of Object.entries(config)) {
    const color = (isDark ? c.theme?.dark : c.theme?.light) ?? c.color;
    if (color) out[`--color-${k}`] = color;
  }
  return out;
}

function useIsDark(): boolean {
  const [isDark, setIsDark] = React.useState(
    () => typeof document !== "undefined" && document.documentElement.classList.contains("dark"),
  );
  React.useEffect(() => {
    const obs = new MutationObserver(() => {
      setIsDark(document.documentElement.classList.contains("dark"));
    });
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => obs.disconnect();
  }, []);
  return isDark;
}

export const ChartContainer = React.forwardRef<
  HTMLDivElement,
  React.ComponentProps<"div"> & {
    config: ChartConfig;
    children: React.ComponentProps<typeof RechartsPrimitive.ResponsiveContainer>["children"];
  }
>(({ className, children, config, style, ...props }, ref) => {
  const isDark = useIsDark();
  const cssVars = React.useMemo(() => buildCSSVars(config, isDark), [config, isDark]);
  return (
    <ChartContext.Provider value={{ config }}>
      <div
        ref={ref}
        style={{ ...cssVars, ...style }}
        className={cn(
          "flex aspect-video justify-center text-xs",
          "[&_.recharts-cartesian-axis-tick_text]:fill-muted-foreground",
          "[&_.recharts-cartesian-grid_line]:stroke-border/60",
          "[&_.recharts-curve.recharts-tooltip-cursor]:stroke-border",
          "[&_.recharts-layer]:outline-hidden",
          "[&_.recharts-rectangle.recharts-tooltip-cursor]:fill-muted",
          "[&_.recharts-sector]:outline-hidden",
          "[&_.recharts-surface]:outline-hidden",
          className,
        )}
        {...props}
      >
        <RechartsPrimitive.ResponsiveContainer>{children}</RechartsPrimitive.ResponsiveContainer>
      </div>
    </ChartContext.Provider>
  );
});
ChartContainer.displayName = "Chart";

export const ChartTooltip = RechartsPrimitive.Tooltip;

type TooltipEntry = {
  dataKey?: string | number;
  name?: string | number;
  value?: number | string;
  color?: string;
  payload?: Record<string, unknown>;
};

export const ChartTooltipContent = React.forwardRef<
  HTMLDivElement,
  React.ComponentProps<"div"> & {
    active?: boolean;
    payload?: TooltipEntry[];
    label?: string | number;
    hideLabel?: boolean;
    indicator?: "line" | "dot" | "dashed";
    labelFormatter?: (
      value: string | number | undefined,
      payload: TooltipEntry[],
    ) => React.ReactNode;
    valueFormatter?: (value: number | string) => React.ReactNode;
  }
>(
  (
    {
      active,
      payload,
      className,
      indicator = "dot",
      hideLabel = false,
      label,
      labelFormatter,
      valueFormatter,
    },
    ref,
  ) => {
    const { config } = useChart();
    if (!active || !payload?.length) return null;

    const labelNode = (() => {
      if (hideLabel) return null;
      const content = labelFormatter ? labelFormatter(label, payload) : label;
      if (content == null) return null;
      return <div className="eyebrow mb-0.5">{content}</div>;
    })();

    return (
      <div
        ref={ref}
        className={cn(
          "min-w-[9rem] rounded-md border border-border-strong bg-popover px-3 py-2 text-xs shadow-xl",
          className,
        )}
      >
        {labelNode}
        <div className="grid gap-1.5">
          {payload.map((item) => {
            const key = `${item.dataKey || item.name || "value"}`;
            const itemConfig = config[key];
            const color = item.color || itemConfig?.color || `var(--color-${key})`;
            const v = item.value;
            return (
              <div key={key} className="flex items-center gap-2">
                <div
                  className={cn(
                    "shrink-0 rounded-[2px]",
                    indicator === "dot" && "h-2.5 w-2.5",
                    indicator === "line" && "h-2.5 w-1",
                    indicator === "dashed" &&
                      "h-2.5 w-2.5 border-[1.5px] border-dashed bg-transparent",
                  )}
                  style={{
                    background: indicator === "dashed" ? "transparent" : color,
                    borderColor: color,
                  }}
                />
                <span className="text-muted-foreground flex-1">
                  {itemConfig?.label || item.name}
                </span>
                {v != null && (
                  <span className="font-mono font-medium tabular text-foreground ml-3">
                    {valueFormatter
                      ? valueFormatter(v)
                      : typeof v === "number"
                        ? v.toLocaleString()
                        : String(v)}
                  </span>
                )}
              </div>
            );
          })}
        </div>
      </div>
    );
  },
);
ChartTooltipContent.displayName = "ChartTooltip";

export const ChartLegend = RechartsPrimitive.Legend;

export const ChartLegendContent = React.forwardRef<
  HTMLDivElement,
  React.ComponentProps<"div"> & {
    payload?: { value?: string | number; dataKey?: string | number; color?: string }[];
    verticalAlign?: "top" | "bottom" | "middle";
  }
>(({ className, payload, verticalAlign = "bottom" }, ref) => {
  const { config } = useChart();
  if (!payload?.length) return null;
  return (
    <div
      ref={ref}
      className={cn(
        "flex items-center justify-center gap-4 flex-wrap text-xs",
        verticalAlign === "top" ? "pb-3" : "pt-3",
        className,
      )}
    >
      {payload.map((item) => {
        const key = `${item.dataKey || "value"}`;
        const itemConfig = config[key];
        return (
          <div key={String(item.value)} className="flex items-center gap-1.5">
            <div
              className="h-2.5 w-2.5 shrink-0 rounded-[2px]"
              style={{ background: item.color }}
            />
            <span className="text-muted-foreground">{itemConfig?.label || item.value}</span>
          </div>
        );
      })}
    </div>
  );
});
ChartLegendContent.displayName = "ChartLegend";
