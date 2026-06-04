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
