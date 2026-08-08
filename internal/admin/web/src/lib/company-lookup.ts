import { apiGet } from "@/lib/api";
import type { InvoiceTitle } from "@/lib/types";

/**
 * Company-name lookup for the 发票抬头 picker.
 *
 * The provider blocks by geography: our servers are offshore and get
 * {"errorCode":301000,"message":"bannedLocation"}, while the same request from
 * a mainland IP succeeds. Our customers are overwhelmingly mainland-based, so
 * their browsers can reach what our backend cannot.
 *
 * Hence: try the browser first, fall back to the server. Verified against the
 * live endpoint from the production origin — it answers preflight with
 * `Access-Control-Allow-Origin: <request origin>` and
 * `Access-Control-Allow-Headers: content-type`, so a cross-origin POST from our
 * page is allowed.
 *
 * The server path stays as the fallback rather than being deleted: it covers
 * customers who are themselves offshore (where the browser hits the same block
 * we do) and deployments that configure a mainland egress proxy, and it holds
 * the shared response cache. If both fail the caller degrades to manual entry —
 * an invoice request must never be blocked by a lookup.
 */

const DIRECT_URL = "https://capi.tianyancha.com/cloud-tempest/search/suggest/v3";
const DIRECT_TIMEOUT_MS = 4000;

export type LookupResult = {
  titles: InvoiceTitle[];
  /** True when neither path could answer — the UI tells the user to type it in. */
  degraded: boolean;
};

type SuggestRow = {
  comName?: string;
  name?: string;
  entName?: string;
  taxCode?: string;
  creditCode?: string;
};

/** The API marks matched substrings with <em>; strip them for display. */
function stripHighlight(s: string): string {
  return s.replace(/<\/?em>/g, "").trim();
}

async function lookupDirect(q: string): Promise<InvoiceTitle[] | null> {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), DIRECT_TIMEOUT_MS);
  try {
    const res = await fetch(DIRECT_URL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ keyword: q }),
      signal: ctl.signal,
    });
    if (!res.ok) return null;
    const j = (await res.json()) as {
      state?: string;
      errorCode?: number;
      data?: SuggestRow[] | unknown;
    };
    // Both the geo-block and the rate-limit arrive as HTTP 200 with an error
    // code in the body, so status alone proves nothing.
    if (j.errorCode !== 0 || j.state !== "ok" || !Array.isArray(j.data)) return null;
    return j.data
      .map((r) => ({
        name: stripHighlight(r.comName ?? r.name ?? r.entName ?? ""),
        tax_no: r.taxCode ?? r.creditCode ?? "",
      }))
      .filter((t) => t.name !== "");
  } catch {
    // Blocked, offline, CORS-refused, or timed out — all mean "ask the server".
    return null;
  } finally {
    clearTimeout(timer);
  }
}

async function lookupViaServer(q: string): Promise<InvoiceTitle[] | null> {
  try {
    const r = await apiGet<{ titles: InvoiceTitle[]; degraded?: boolean }>(
      `/invoice/title-suggest?q=${encodeURIComponent(q)}`,
    );
    if (r.degraded) return null;
    return r.titles ?? [];
  } catch {
    return null;
  }
}

export async function lookupCompany(q: string): Promise<LookupResult> {
  // A direct answer is final, including an empty one: the server queries the
  // same provider with no extra sources of its own, so it can only agree or
  // fail. Asking it anyway would add a doomed round-trip (up to the timeout)
  // to every search that genuinely matches nothing — the slowest path for the
  // least useful outcome.
  const direct = await lookupDirect(q);
  if (direct) return { titles: direct, degraded: false };

  const viaServer = await lookupViaServer(q);
  if (viaServer) return { titles: viaServer, degraded: false };
  return { titles: [], degraded: true };
}
