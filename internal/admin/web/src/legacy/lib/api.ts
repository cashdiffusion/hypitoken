const TOKEN_KEY = "cpa.admin.token";
export const getToken = (): string => localStorage.getItem(TOKEN_KEY) || "";
export const setToken = (t: string): void => {
  if (t) localStorage.setItem(TOKEN_KEY, t);
  else localStorage.removeItem(TOKEN_KEY);
};

// Derive the mount prefix from window.location so the SPA works at any
// admin_path the operator picked (e.g. /mgmt-console, /mngt-ctrl).
// In Vite dev mode the SPA is served at / but the proxy forwards
// /mgmt-console/api; we detect that and hardcode the dev prefix.
export const ADMIN_BASE: string = (() => {
  const p = window.location.pathname || "/";
  if (import.meta.env.DEV) return "/mgmt-console";
  return p.replace(/\/(assets(\/.*)?|app(\/.*)?)?\/?$/, "") || "";
})();

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
  const res = await fetch(p, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      "X-Admin-Token": token,
      ...(opts.headers || {}),
    },
  });
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
