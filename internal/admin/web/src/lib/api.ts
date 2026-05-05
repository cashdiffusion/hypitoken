// SaaS API client. JWT-based; token persisted in localStorage.

const TOKEN_KEY = "hypi.jwt";
const USER_KEY = "hypi.user";

export const getJWT = (): string => localStorage.getItem(TOKEN_KEY) || "";
export const setJWT = (t: string): void => {
  if (t) localStorage.setItem(TOKEN_KEY, t);
  else localStorage.removeItem(TOKEN_KEY);
};

export const getCachedUser = () => {
  const raw = localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
};
export const setCachedUser = (u: any): void => {
  if (u) localStorage.setItem(USER_KEY, JSON.stringify(u));
  else localStorage.removeItem(USER_KEY);
};

export const logout = () => {
  setJWT("");
  setCachedUser(null);
};

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

const BASE = "/api/v2";

export async function api<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const token = getJWT();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...((opts.headers as Record<string, string>) || {}),
  };
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(BASE + path, { ...opts, headers });
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!res.ok) {
    const msg = (data && data.error) || `HTTP ${res.status}`;
    throw new ApiError(msg, res.status);
  }
  return data as T;
}

export const apiGet = <T = any>(p: string) => api<T>(p, { method: "GET" });
export const apiPost = <T = any>(p: string, body?: any) =>
  api<T>(p, { method: "POST", body: body == null ? undefined : JSON.stringify(body) });
export const apiPatch = <T = any>(p: string, body?: any) =>
  api<T>(p, { method: "PATCH", body: body == null ? undefined : JSON.stringify(body) });
export const apiDelete = <T = any>(p: string) => api<T>(p, { method: "DELETE" });
