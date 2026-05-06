import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export const fmtInt = (n: number | null | undefined): string =>
  n == null ? "—" : Number(n).toLocaleString();

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

// Client-side sk-<48 alphanumerics> generator mirroring clienttoken.Generate().
export function generateSkToken(): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  const n = 48;
  const buf = new Uint32Array(n);
  crypto.getRandomValues(buf);
  let out = "sk-";
  for (let i = 0; i < n; i++) {
    out += alphabet[buf[i]! % alphabet.length];
  }
  return out;
}

// Parse an ISO-week key ("YYYY-Www") into a "Mon D – Mon D" UTC range.
export function isoWeekRange(key: string | undefined | null): string {
  if (!key) return "";
  const m = /^(\d{4})-W(\d{2})$/.exec(key);
  if (!m) return "";
  const year = parseInt(m[1]!, 10);
  const week = parseInt(m[2]!, 10);
  const jan4 = new Date(Date.UTC(year, 0, 4));
  const jan4Dow = jan4.getUTCDay() || 7;
  const start = new Date(jan4);
  start.setUTCDate(jan4.getUTCDate() - (jan4Dow - 1) + (week - 1) * 7);
  const end = new Date(start);
  end.setUTCDate(start.getUTCDate() + 6);
  const fmt = (d: Date) =>
    d.toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" });
  return `${fmt(start)} – ${fmt(end)}`;
}

// Precise countdown until an ISO timestamp. "2h 34m", "3d 4h 12m", or
// "ready now" once it has elapsed. Intended for rate-limit reset timers
// where the user wants to know how much longer to wait.
export function fmtCountdown(s: string | Date | null | undefined): string {
  if (!s) return "—";
  const d = typeof s === "string" ? new Date(s) : s;
  if (isNaN(d.getTime())) return "—";
  const diff = d.getTime() - Date.now();
  if (diff <= 0) return "ready now";
  const totalSec = Math.floor(diff / 1000);
  const days = Math.floor(totalSec / 86400);
  const hours = Math.floor((totalSec % 86400) / 3600);
  const mins = Math.floor((totalSec % 3600) / 60);
  const secs = totalSec % 60;
  const parts: string[] = [];
  if (days) parts.push(`${days}d`);
  if (days || hours) parts.push(`${hours}h`);
  if (days || hours || mins) parts.push(`${mins}m`);
  if (!days && !hours) parts.push(`${secs}s`);
  return parts.join(" ");
}

export function fmtLocalTime(s: string | Date | null | undefined): string {
  if (!s) return "";
  const d = typeof s === "string" ? new Date(s) : s;
  if (isNaN(d.getTime())) return "";
  const sameYear = d.getFullYear() === new Date().getFullYear();
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    year: sameYear ? undefined : "numeric",
  });
}

export function fmtDate(s: string | Date | null | undefined): string {
  if (!s) return "—";
  const d = typeof s === "string" ? new Date(s) : s;
  if (isNaN(d.getTime())) return "—";
  const diff = d.getTime() - Date.now();
  const abs = Math.abs(diff);
  const m = Math.round(abs / 60000);
  const h_ = Math.round(abs / 3600000);
  const day = Math.round(abs / 86400000);
  let rel: string;
  if (abs < 60000) rel = "now";
  else if (m < 60) rel = `${m}m`;
  else if (h_ < 48) rel = `${h_}h`;
  else rel = `${day}d`;
  return diff < 0 ? `${rel} ago` : `in ${rel}`;
}

export function modelMapToText(m: Record<string, string> | null | undefined): string {
  if (!m) return "";
  return Object.keys(m)
    .sort()
    .map((k) => `${k} = ${m[k] || ""}`)
    .join("\n");
}

export function textToModelMap(text: string): { map: Record<string, string>; errors: string[] } {
  const out: Record<string, string> = {};
  const errors: string[] = [];
  (text || "").split(/\r?\n/).forEach((rawLine, i) => {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) return;
    const eq = line.indexOf("=");
    if (eq < 0) {
      errors.push(`line ${i + 1}: missing '='`);
      return;
    }
    const k = line.slice(0, eq).trim();
    const v = line.slice(eq + 1).trim();
    if (!k) {
      errors.push(`line ${i + 1}: empty client model name`);
      return;
    }
    out[k] = v;
  });
  return { map: out, errors };
}
