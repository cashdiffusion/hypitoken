import { ApiError } from "./api";

export interface StatusOverview {
  counts: {
    total: number;
    healthy: number;
    quota: number;
    unhealthy: number;
    disabled: number;
    oauth: number;
    apikey: number;
    models: number;
  };
  window_24h: {
    requests: number;
    cost_usd: number;
    errors: number;
  };
  auths: {
    kind: "oauth" | "apikey";
    label?: string;
    group?: string;
    healthy: boolean;
    disabled?: boolean;
    quota_exceeded?: boolean;
    hard_failure?: boolean;
  }[];
}

export interface StatusRecent {
  ts: string;
  model?: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  status: number;
  duration_ms: number;
  stream?: boolean;
  auth_label?: string;
  auth_kind?: string;
}

export interface StatusAgg {
  count: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_create_tokens: number;
  cost_usd: number;
  errors: number;
  total_duration_ms: number;
}

export interface StatusWeekEntry {
  week: string;
  cost: {
    tokens: Record<string, number>;
    cost_usd: number;
    requests: number;
  };
}

export interface StatusDailyEntry {
  date: string;
  cost_usd: number;
  requests: number;
}

export interface StatusTokenResult {
  masked: string;
  found: boolean;
  name?: string;
  group?: string;
  weekly_limit: number;
  weekly_used_usd: number;
  blocked: boolean;
  total: { tokens: Record<string, number>; cost_usd: number; requests: number };
  weekly?: StatusWeekEntry[];
  daily?: StatusDailyEntry[];
  last_used?: string;
  recent?: StatusRecent[];
  recent_total?: number;
  window_24h?: StatusAgg;
}

export interface StatusHistoryResp {
  entries: StatusRecent[];
  total: number;
  offset: number;
  limit: number;
}

async function fetchJSON<T>(
  path: string,
  opts: RequestInit = {},
): Promise<T> {
  const res = await fetch(path, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
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

export function loadStatusOverview(): Promise<StatusOverview> {
  return fetchJSON<StatusOverview>("/status/api/overview");
}

// Shape matches internal/admin/status.go statusDashboard. by_client keys
// are deterministic pseudonyms (Alice/Bob/...), not real customer labels.
export interface StatusDashboardResp {
  pool: {
    total: number;
    healthy: number;
    quota: number;
    unhealthy: number;
    disabled: number;
    oauth: number;
    apikey: number;
  };
  pricing?: import("./types").Pricing;
  requests_14d: {
    summary: import("./types").RequestAgg;
    by_client: Record<string, import("./types").RequestAgg>;
    by_model: Record<string, import("./types").RequestAgg>;
    by_day: Record<string, import("./types").RequestAgg>;
  };
  requests_all: {
    summary: import("./types").RequestAgg;
    by_client: Record<string, import("./types").RequestAgg>;
    by_model: Record<string, import("./types").RequestAgg>;
    by_day: Record<string, import("./types").RequestAgg>;
  };
  hourly_24h: import("./types").HourBucket[];
}

export function loadStatusDashboard(): Promise<StatusDashboardResp> {
  return fetchJSON<StatusDashboardResp>("/status/api/dashboard");
}

export function queryStatusTokens(
  tokens: string[],
): Promise<{ results: StatusTokenResult[] }> {
  return fetchJSON<{ results: StatusTokenResult[] }>("/status/api/query", {
    method: "POST",
    body: JSON.stringify({ tokens }),
  });
}

export function queryStatusHistory(args: {
  token: string;
  offset?: number;
  limit?: number;
  from?: string;
  to?: string;
}): Promise<StatusHistoryResp> {
  return fetchJSON<StatusHistoryResp>("/status/api/history", {
    method: "POST",
    body: JSON.stringify(args),
  });
}

const TOKENS_KEY = "cpa.status.tokens";
export function loadSavedTokens(): string[] {
  try {
    const raw = localStorage.getItem(TOKENS_KEY);
    if (!raw) return [];
    const arr = JSON.parse(raw);
    return Array.isArray(arr) ? arr.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}
export function saveSavedTokens(tokens: string[]): void {
  localStorage.setItem(TOKENS_KEY, JSON.stringify(tokens));
}
