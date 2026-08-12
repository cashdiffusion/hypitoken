// Discovers all `.md` files in src/content/docs/{en,zh}/, parses
// frontmatter, and exposes them as DocSection[] keyed by language.
// Edit the .md files directly — no rebuild of TS code required.

export interface DocSection {
  slug: string; // url segment (matches /docs/:slug)
  title: string;
  group: string;
  intro?: string;
  order: number; // sort key
  body: string; // raw markdown (without frontmatter)
}

// Vite eagerly imports every .md file as raw text at build time.
const enModules = import.meta.glob<string>("../content/docs/en/*.md", {
  eager: true,
  query: "?raw",
  import: "default",
});
const zhModules = import.meta.glob<string>("../content/docs/zh/*.md", {
  eager: true,
  query: "?raw",
  import: "default",
});

function parseFrontmatter(raw: string): { meta: Record<string, string>; body: string } {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n+([\s\S]*)$/.exec(raw);
  if (!m) return { meta: {}, body: raw };
  const meta: Record<string, string> = {};
  for (const line of (m[1] ?? "").split(/\r?\n/)) {
    const i = line.indexOf(":");
    if (i <= 0) continue;
    const k = line.slice(0, i).trim();
    let v = line.slice(i + 1).trim();
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      v = v.slice(1, -1);
    }
    meta[k] = v;
  }
  return { meta, body: m[2] ?? "" };
}

function buildDocs(modules: Record<string, string>): DocSection[] {
  return Object.values(modules)
    .map((raw) => {
      const { meta, body } = parseFrontmatter(raw);
      return {
        slug: meta.slug || "untitled",
        title: meta.title || meta.slug || "Untitled",
        group: meta.group || "Reference",
        intro: meta.intro,
        order: parseInt(meta.order || "999", 10),
        body,
      };
    })
    .sort((a, b) => a.order - b.order);
}

const DOCS_EN = buildDocs(enModules);
const DOCS_ZH = buildDocs(zhModules);

// docsFor returns the localized doc table. Falls back to English when the
// language has no translation for a slug — better partial coverage than a
// missing page.
export function docsFor(lang: string | undefined): DocSection[] {
  const norm = (lang || "en").toLowerCase();
  if (norm.startsWith("zh") && DOCS_ZH.length > 0) {
    // Merge: prefer zh, fall back to en for any slug without zh translation.
    const zhSlugs = new Set(DOCS_ZH.map((d) => d.slug));
    const fallback = DOCS_EN.filter((d) => !zhSlugs.has(d.slug));
    return [...DOCS_ZH, ...fallback].sort((a, b) => a.order - b.order);
  }
  return DOCS_EN;
}

// docsAsMarkdown concatenates the localized doc set into one document.
//
// The audience is an AI agent, not a reader: users copy this and paste it into
// Claude Code or Codex so the agent performs the setup for them. Hence the
// leading instruction block — without it a pasted wall of prose reads as a
// question about documentation rather than a task to carry out.
export function docsAsMarkdown(lang?: string): string {
  const zh = (lang || "en").toLowerCase().startsWith("zh");
  const body = docsFor(lang)
    .map((d) => `## ${d.title}\n\n${d.body.trim()}`)
    .join("\n\n---\n\n");
  const preamble = zh
    ? [
        "# HypiToken 接入文档（全文）",
        "",
        "以下是 HypiToken API 网关的完整接入说明。请据此为我完成配置：",
        "识别我的操作系统与已安装的客户端，写入对应的配置文件或环境变量，",
        "然后运行文档中的验证命令确认接入成功。密钥需要我提供，不要凭空编造。",
      ]
    : [
        "# HypiToken documentation (full text)",
        "",
        "Below is the complete setup guide for the HypiToken API gateway. Use it to",
        "configure my machine: detect my OS and which clients I have installed, write",
        "the matching config files or environment variables, then run the verification",
        "commands from the docs. Ask me for the API key — never invent one.",
      ];
  return [...preamble, "", "---", "", body].join("\n");
}

// Backwards-compat: `DOCS` retained as the default-language table for
// legacy importers. Prefer docsFor(lang) at the call site.
export const DOCS: DocSection[] = DOCS_EN;

export const DOC_GROUPS = Array.from(new Set(DOCS.map((d) => d.group)));

export function findDoc(slug: string, lang?: string): DocSection | undefined {
  return docsFor(lang).find((d) => d.slug === slug);
}

// Slugify a heading text so the TOC anchors match what react-markdown
// generates (or our renderer assigns).
//
// Must keep Unicode letters/numbers (CJK included): the docs are bilingual and
// a `\w`-only filter would strip every Chinese character, collapsing headings
// like "这是什么" to an empty id. That broke anchor navigation AND the
// scroll-spy (an empty activeID matched every heading at once, so the whole
// TOC lit up). `\p{L}\p{N}` with the `u` flag preserves CJK while still
// dropping punctuation.
export function slugify(text: string): string {
  return text
    .toLowerCase()
    .trim()
    .replace(/[^\p{L}\p{N}\s-]/gu, "")
    .replace(/\s+/g, "-");
}
