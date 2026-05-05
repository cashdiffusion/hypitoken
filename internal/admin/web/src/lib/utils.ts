import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
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
  // eslint-disable-next-line @typescript-eslint/no-deprecated
  (document as any).execCommand("copy");
  document.body.removeChild(ta);
}
