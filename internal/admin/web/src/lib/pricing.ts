// canonicalProvider mirrors pricing.canonicalProvider in
// internal/pricing/pricing.go — empty/"anthropic"/"claude" → anthropic;
// "openai"/"codex"/"chatgpt" → openai; everything else passes through.
export function canonicalProvider(p: string | undefined | null): string {
  const v = (p || "").toLowerCase().trim();
  if (v === "" || v === "anthropic" || v === "claude") return "anthropic";
  if (v === "openai" || v === "codex" || v === "chatgpt") return "openai";
  return v;
}

// lookupPriceCard resolves a (provider, model) pair against a price map
// keyed by canonical "<provider>/<model>" (as returned by /api/v2/me/requests).
// Resolution order: exact key → hyphen-prefix walk under the same provider →
// undefined. Always pair the model with its provider — the catalog keys are
// stored as "openai/gpt-5", so a bare-model lookup against a mixed
// provider/model map will silently miss for OpenAI rows.
export function lookupPriceCard<T>(
  prices: Record<string, T> | undefined | null,
  provider: string | undefined | null,
  model: string | undefined | null,
): T | undefined {
  if (!prices) return undefined;
  const prov = canonicalProvider(provider);
  let m = (model || "").toLowerCase().trim();
  if (m.endsWith(")")) {
    const i = m.lastIndexOf("(");
    if (i > 0) m = m.slice(0, i).trim();
  }
  if (!m) return undefined;
  const full = `${prov}/${m}`;
  if (prices[full]) return prices[full];
  for (let i = m.lastIndexOf("-"); i > 0; i = m.lastIndexOf("-", i - 1)) {
    const card = prices[`${prov}/${m.slice(0, i)}`];
    if (card) return card;
  }
  return undefined;
}
