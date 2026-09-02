import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";
import { ApiError } from "@/lib/api";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// errMsg / errStatus normalize the `unknown` value of a strict-mode catch
// binding into the message + HTTP status the UI wants, without sprinkling
// `e: any` casts at every call site.
export function errMsg(e: unknown, fallback = "操作失败"): string {
  if (e instanceof Error) return e.message || fallback;
  if (typeof e === "string") return e || fallback;
  return fallback;
}

export function errStatus(e: unknown): number | undefined {
  return e instanceof ApiError ? e.status : undefined;
}

export const fmtInt = (n: number | null | undefined): string => {
  if (n == null) return "—";
  const v = Number(n);
  if (!Number.isFinite(v)) return "—";
  return v.toLocaleString();
};

// fmtCompact: abbreviate large integers (942K / 14.9M / 6.64B / 1.30T) so big
// token counts never overflow a fixed-width KPI tile. Under 10,000 it falls
// back to the exact grouped form so small, meaningful numbers stay precise.
// 3 significant figures. Null/NaN → "—".
export const fmtCompact = (n: number | null | undefined): string => {
  if (n == null) return "—";
  const v = Number(n);
  if (!Number.isFinite(v)) return "—";
  const abs = Math.abs(v);
  if (abs < 10_000) return v.toLocaleString();
  const units = [
    { d: 1e12, s: "T" },
    { d: 1e9, s: "B" },
    { d: 1e6, s: "M" },
    { d: 1e3, s: "K" },
  ];
  for (const u of units) {
    if (abs >= u.d) {
      const x = v / u.d;
      const scaled = abs / u.d;
      const str = scaled >= 100 ? x.toFixed(0) : scaled >= 10 ? x.toFixed(1) : x.toFixed(2);
      return str + u.s;
    }
  }
  return v.toLocaleString();
};

// fmtUSD: 2 decimals once the figure is "real money" ($0.01+), otherwise
// 4 decimals so per-request micro-costs stay visible. Zero/missing → "—".
export const fmtUSD = (n: number | null | undefined): string => {
  if (n == null) return "—";
  const v = Number(n);
  if (!Number.isFinite(v)) return "—";
  if (v === 0) return "$0";
  return v >= 0.01 ? `$${v.toFixed(2)}` : `$${v.toFixed(4)}`;
};

// navigator.clipboard requires a secure context; fall back to a legacy
// textarea-select-copy approach over plain HTTP on a LAN IP.
export async function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // fall through to legacy path
    }
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.left = "-9999px";
  document.body.appendChild(ta);
  ta.select();
  document.execCommand("copy");
  document.body.removeChild(ta);
}

/** fmtHours renders a span in hours as "45m" / "7.5h" / "2d 12h". */
export const fmtHours = (h: number | null | undefined): string => {
  if (h == null || !Number.isFinite(h)) return "—";
  if (h < 1) return `${Math.round(h * 60)}m`;
  if (h < 48) return `${h < 10 ? h.toFixed(1) : Math.round(h)}h`;
  const d = Math.floor(h / 24);
  const r = Math.round(h - d * 24);
  return r ? `${d}d ${r}h` : `${d}d`;
};
