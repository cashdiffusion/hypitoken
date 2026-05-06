// Legacy /mgmt-console API client. Originally took an X-Admin-Token; we
// now SSO via the SaaS JWT (`hypi.jwt`) when present, so a logged-in
// SaaS user can hit the legacy panel without learning the operator
// password. Falls back to the legacy token if it's set explicitly.
const LEGACY_TOKEN_KEY = "cpa.admin.token";
const SAAS_TOKEN_KEY = "hypi.jwt";

// getToken returns whatever credential is currently usable for the
// legacy console — preferring the SaaS JWT when the user is signed in.
// `setToken` still writes to the legacy store so explicit overrides keep
// working (operator with the original token but no SaaS account).
export const getToken = (): string =>
  localStorage.getItem(SAAS_TOKEN_KEY) ||
  localStorage.getItem(LEGACY_TOKEN_KEY) ||
  "";
export const setToken = (t: string): void => {
  if (t) localStorage.setItem(LEGACY_TOKEN_KEY, t);
  else localStorage.removeItem(LEGACY_TOKEN_KEY);
};

// Always hit the live admin path under /mgmt-console. We don't need the
// dynamic ADMIN_BASE inference the standalone legacy SPA had — the SaaS
// shell only ever serves at the same origin as the backend.
export const ADMIN_BASE = "/mgmt-console";

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export async function api<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const token = getToken();
  let p = path;
  if (p.startsWith("/admin/")) p = ADMIN_BASE + p.slice("/admin".length);
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...((opts.headers as Record<string, string>) || {}),
  };
  if (token) {
    // Send both flavors. The legacy gate looks for X-Admin-Token first;
    // the SSO bridge sniffs Authorization. Sending both means the same
    // request works whether `token` is a SaaS JWT or the legacy password.
    headers["X-Admin-Token"] = token;
    if (!headers["Authorization"]) {
      headers["Authorization"] = "Bearer " + token;
    }
  }
  const res = await fetch(p, { ...opts, headers });
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!res.ok) {
    throw new ApiError((data && data.error) || `HTTP ${res.status}`, res.status);
  }
  return data as T;
}
