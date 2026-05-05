// Discovers all `.md` files in src/content/docs/, parses frontmatter, and
// exposes them as DocSection[]. Edit the .md files directly — no rebuild
// of TS code required.

export interface DocSection {
  slug: string;       // url segment (matches /docs/:slug)
  title: string;
  group: string;
  intro?: string;
  order: number;      // sort key
  body: string;       // raw markdown (without frontmatter)
}

// Vite eagerly imports every .md file as raw text at build time.
const modules = import.meta.glob<string>("../content/docs/*.md", {
  eager: true,
  query: "?raw",
  import: "default",
});

function parseFrontmatter(raw: string): { meta: Record<string, string>; body: string } {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n+([\s\S]*)$/.exec(raw);
  if (!m) return { meta: {}, body: raw };
  const meta: Record<string, string> = {};
  for (const line of m[1]!.split(/\r?\n/)) {
    const i = line.indexOf(":");
    if (i <= 0) continue;
    const k = line.slice(0, i).trim();
    let v = line.slice(i + 1).trim();
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      v = v.slice(1, -1);
    }
    meta[k] = v;
  }
  return { meta, body: m[2]! };
}

export const DOCS: DocSection[] = Object.values(modules)
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

export const DOC_GROUPS = Array.from(new Set(DOCS.map((d) => d.group)));

export function findDoc(slug: string): DocSection | undefined {
  return DOCS.find((d) => d.slug === slug);
}

// Slugify a heading text so the TOC anchors match what react-markdown
// generates (or our renderer assigns).
export function slugify(text: string): string {
  return text
    .toLowerCase()
    .trim()
    .replace(/[^\w\s-]/g, "")
    .replace(/\s+/g, "-");
}
