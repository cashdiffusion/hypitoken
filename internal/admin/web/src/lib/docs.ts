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

// docAsMarkdown renders one page as a standalone markdown document.
//
// This is the clipboard payload, and its usual destination is an agent's
// prompt window rather than a text editor: users copy a page and ask Claude
// Code or Codex to carry out the setup it describes. Hence the title heading —
// the body's own headings start at h2, so without it the pasted text has no
// statement of what it is about.
//
// Deliberately nothing more than that. An instruction preamble would make the
// payload something other than what the button says it copies.
export function docAsMarkdown(doc: DocSection): string {
  return `# ${doc.title}\n\n${doc.body.trim()}\n`;
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
