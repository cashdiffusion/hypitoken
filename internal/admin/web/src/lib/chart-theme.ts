import type { ChartConfig } from "@/components/ui/chart";

/* Shared chart palette.
 *
 * ChartContainer turns a { theme: { light, dark } } entry into a --color-<key>
 * CSS variable and swaps it on theme change, so every series here is dual-mode
 * for free — never pass a flat `color`, and never write a dark: variant.
 *
 * The globals.css tokens stop at --primary/--success/--warning; there are no
 * --chart-N slots. Rather than add some (which would ripple into the legacy
 * dashboard), the palette lives here as oklch literals: low-L / high-C in light
 * mode, high-L in dark, matching what the legacy board already proved reads well
 * on both surfaces.
 */

const SPEND_LIGHT = "oklch(0.38 0.09 215)"; // = --primary (light): deep teal
const SPEND_DARK = "oklch(0.82 0.16 145)"; // = --primary (dark): bright green

export const spendConfig = {
  spent_usd: {
    label: "花费",
    theme: { light: SPEND_LIGHT, dark: SPEND_DARK },
  },
  charge_events: {
    label: "计费笔数",
    theme: { light: "oklch(0.48 0.10 215)", dark: "oklch(0.72 0.14 215)" },
  },
} satisfies ChartConfig;

/* CATEGORY — for per-model / per-tag slices, where each series is a distinct
 * nominal category and the ordering carries no meaning. Five hues, evenly spread,
 * each legible against both surfaces. */
export const CATEGORY = [
  { light: "oklch(0.52 0.12 215)", dark: "oklch(0.80 0.15 160)" },
  { light: "oklch(0.58 0.17 285)", dark: "oklch(0.75 0.17 285)" },
  { light: "oklch(0.62 0.15 70)", dark: "oklch(0.82 0.16 72)" },
  { light: "oklch(0.62 0.19 330)", dark: "oklch(0.78 0.17 330)" },
  { light: "oklch(0.55 0.18 25)", dark: "oklch(0.68 0.20 25)" },
] as const;

/** categoryConfig builds a ChartConfig assigning each key a stable palette slot. */
export function categoryConfig(keys: string[]): ChartConfig {
  const cfg: ChartConfig = {};
  keys.forEach((key, i) => {
    cfg[key] = { label: key, theme: { ...CATEGORY[i % CATEGORY.length] } };
  });
  return cfg;
}

/** Palette slot for index i — for <Cell fill={categoryVar(i)} /> inside a Pie. */
export const categoryVar = (i: number) => `var(--color-cat-${i % CATEGORY.length})`;

/** The 5 category slots as a ChartConfig, keyed cat-0..cat-4. */
export const categorySlots: ChartConfig = Object.fromEntries(
  CATEGORY.map((theme, i) => [`cat-${i}`, { label: `#${i}`, theme: { ...theme } }]),
);
