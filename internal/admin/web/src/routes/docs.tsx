import { BookOpen, Check, ChevronDown, ChevronRight, Copy, Hash } from "lucide-react";
import React, { type ReactNode, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown, { type ExtraProps } from "react-markdown";
import { Link, NavLink, useLocation, useNavigate, useParams } from "react-router-dom";
import rehypeHighlight from "rehype-highlight";
import rehypeRaw from "rehype-raw";
import remarkGfm from "remark-gfm";
import { type DocSection, docsFor, slugify } from "@/lib/docs";
import { isOSGroup, type OS, OS_VALUES, OSProvider, useOS } from "@/lib/use-os";
import { cn, copyToClipboard } from "@/lib/utils";
import "highlight.js/styles/github-dark.css";

// Icons for the global OS switcher.
const OS_ICONS: Record<OS, string> = { macOS: "", Windows: "⊞", Linux: "🐧" };

export default function DocsLayout() {
  const params = useParams();
  const { i18n } = useTranslation();
  const lang = i18n.resolvedLanguage || i18n.language;
  const docs = useMemo(() => docsFor(lang), [lang]);
  const groups = useMemo(() => Array.from(new Set(docs.map((d) => d.group))), [docs]);
  const slug = (params.slug as string) || (docs[0]?.slug ?? "quick-start");
  // biome-ignore lint/style/noNonNullAssertion: docs is always non-empty (at least one built-in page)
  const section = docs.find((d) => d.slug === slug) || docs[0]!;
  const idx = docs.findIndex((d) => d.slug === section.slug);
  const prev = docs[idx - 1];
  const next = docs[idx + 1];

  // Extract h2/h3 from the markdown body for the right-hand TOC.
  // biome-ignore lint/correctness/useExhaustiveDependencies: section.slug is the stable change trigger; section.body always changes with it
  const headings = useMemo(() => collectHeadings(section.body), [section.slug]);
  // Only surface the global OS switcher on pages that actually contain
  // OS-specific tab blocks — elsewhere it would have nothing to control.
  const hasOSContent = useMemo(
    () => /data-tab="(macOS|Windows|Linux)"/.test(section.body),
    [section.body],
  );
  const [activeID, setActiveID] = useState<string>(headings[0]?.id ?? "");
  // Mobile-only nav drawer (page list + TOC). Closes whenever the route
  // changes so tapping a link dismisses it.
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: collapse the drawer on every page switch
  useEffect(() => {
    setMobileNavOpen(false);
  }, [section.slug]);

  useEffect(() => {
    if (!headings.length) return;
    const obs = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible[0]) setActiveID((visible[0].target as HTMLElement).id);
      },
      { rootMargin: "-90px 0px -60% 0px", threshold: 0.1 },
    );
    headings.forEach((h) => {
      const el = document.getElementById(h.id);
      if (el) obs.observe(el);
    });
    return () => obs.disconnect();
  }, [headings]);

  return (
    <OSProvider>
      <div className="mx-auto max-w-7xl px-4 md:px-6">
        <div className="grid gap-10 py-10 md:grid-cols-[14rem_minmax(0,1fr)_12rem] md:gap-12 lg:gap-16">
          {/* Sidebar */}
          <aside className="hidden md:block">
            <nav className="sticky top-24 space-y-6">
              {groups.map((g) => (
                <div key={g}>
                  <div className="mb-2 px-2 text-xs font-mono uppercase tracking-wider text-muted-foreground">
                    {g}
                  </div>
                  <ul className="space-y-0.5">
                    {docs
                      .filter((d) => d.group === g)
                      .map((d) => (
                        <li key={d.slug}>
                          <NavLink
                            to={`/docs/${d.slug}`}
                            viewTransition
                            className={({ isActive }) =>
                              cn(
                                "block rounded-md px-2 py-1.5 text-sm transition-colors",
                                isActive
                                  ? "bg-primary/10 font-medium text-primary"
                                  : "text-muted-foreground hover:bg-accent hover:text-foreground",
                              )
                            }
                          >
                            {d.title}
                          </NavLink>
                        </li>
                      ))}
                  </ul>
                </div>
              ))}
            </nav>
          </aside>

          {/* Main */}
          <main className="min-w-0">
            {/* Mobile nav — sidebar + TOC collapse into a sticky drawer since
                  both asides are hidden below md. */}
            <MobileDocNav
              docs={docs}
              groups={groups}
              section={section}
              headings={headings}
              activeID={activeID}
              open={mobileNavOpen}
              setOpen={setMobileNavOpen}
            />
            {/* Desktop breadcrumb + OS switcher. Hidden on mobile — the
                  sticky MobileDocNav above already shows the breadcrumb. */}
            <div className="mb-8 hidden items-center justify-between gap-3 md:flex">
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <BookOpen className="h-3.5 w-3.5" />
                <span>{section.group}</span>
                <ChevronRight className="h-3.5 w-3.5" />
                <span className="text-foreground">{section.title}</span>
              </div>
              {hasOSContent && <OSSwitcher className="shrink-0" />}
            </div>
            {/* OS switcher on mobile, shown only on pages with OS tab blocks. */}
            {hasOSContent && (
              <div className="mb-6 flex md:hidden">
                <OSSwitcher />
              </div>
            )}
            <h1 className="font-display text-4xl font-semibold tracking-tight md:text-5xl">
              {section.title}
            </h1>
            {section.intro && <p className="mt-4 text-lg text-muted-foreground">{section.intro}</p>}

            <article className="hypi-prose mt-8">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                rehypePlugins={[
                  rehypeRaw,
                  [rehypeHighlight, { detect: true, ignoreMissing: true }],
                ]}
                components={mdComponents}
              >
                {section.body}
              </ReactMarkdown>
            </article>

            <div className="mt-16 grid gap-3 sm:grid-cols-2">
              {prev ? (
                <Link
                  to={`/docs/${prev.slug}`}
                  viewTransition
                  className="glass rounded-xl p-4 transition-transform hover:-translate-y-0.5"
                >
                  <div className="text-xs uppercase tracking-wider text-muted-foreground">
                    ← Previous
                  </div>
                  <div className="mt-1 font-display text-lg font-medium">{prev.title}</div>
                </Link>
              ) : (
                <div />
              )}
              {next ? (
                <Link
                  to={`/docs/${next.slug}`}
                  viewTransition
                  className="glass rounded-xl p-4 text-right transition-transform hover:-translate-y-0.5"
                >
                  <div className="text-xs uppercase tracking-wider text-muted-foreground">
                    Next →
                  </div>
                  <div className="mt-1 font-display text-lg font-medium">{next.title}</div>
                </Link>
              ) : (
                <div />
              )}
            </div>
          </main>

          {/* TOC */}
          <aside className="hidden lg:block">
            <div className="sticky top-24">
              {headings.length > 0 && (
                <>
                  <div className="mb-3 px-2 text-xs font-mono uppercase tracking-wider text-muted-foreground">
                    On this page
                  </div>
                  <ul className="space-y-0.5 border-l border-border">
                    {headings.map((h) => (
                      <li key={h.id} className={h.level === 3 ? "pl-3" : ""}>
                        <a
                          href={`#${h.id}`}
                          className={cn(
                            "-ml-px block border-l-2 px-3 py-1 text-sm transition-colors",
                            activeID === h.id
                              ? "border-primary text-primary"
                              : "border-transparent text-muted-foreground hover:text-foreground",
                          )}
                        >
                          {h.text}
                        </a>
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </div>
          </aside>
        </div>
      </div>
    </OSProvider>
  );
}

// Mobile doc navigation. The desktop sidebar (page list) and right-hand TOC are
// both hidden below md, leaving phones with no way to jump between pages or
// sections — only the prev/next footer. This sticky drawer restores both: a
// compact bar showing the current page that expands into the full grouped page
// list plus the current page's headings.
function MobileDocNav({
  docs,
  groups,
  section,
  headings,
  activeID,
  open,
  setOpen,
}: {
  docs: DocSection[];
  groups: string[];
  section: DocSection;
  headings: Array<{ id: string; text: string; level: 2 | 3 }>;
  activeID: string;
  open: boolean;
  setOpen: (v: boolean) => void;
}) {
  return (
    <div className="sticky top-16 z-30 -mx-4 mb-8 border-b border-border bg-background/85 backdrop-blur md:hidden">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
      >
        <span className="flex min-w-0 items-center gap-2 text-sm">
          <BookOpen className="h-4 w-4 shrink-0 text-primary" />
          <span className="truncate text-muted-foreground">{section.group}</span>
          <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate font-medium text-foreground">{section.title}</span>
        </span>
        <ChevronDown
          className={cn(
            "h-4 w-4 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      {open && (
        <div className="max-h-[70vh] overflow-y-auto border-t border-border px-4 pb-5 pt-3">
          {groups.map((g) => (
            <div key={g} className="mb-4">
              <div className="mb-1.5 text-xs font-mono uppercase tracking-wider text-muted-foreground">
                {g}
              </div>
              <ul className="space-y-0.5">
                {docs
                  .filter((d) => d.group === g)
                  .map((d) => (
                    <li key={d.slug}>
                      <NavLink
                        to={`/docs/${d.slug}`}
                        className={({ isActive }) =>
                          cn(
                            "block rounded-md px-2 py-1.5 text-sm transition-colors",
                            isActive
                              ? "bg-primary/10 font-medium text-primary"
                              : "text-muted-foreground hover:bg-accent hover:text-foreground",
                          )
                        }
                      >
                        {d.title}
                      </NavLink>
                    </li>
                  ))}
              </ul>
            </div>
          ))}
          {headings.length > 0 && (
            <div className="mt-1 border-t border-border pt-3">
              <div className="mb-1.5 text-xs font-mono uppercase tracking-wider text-muted-foreground">
                On this page
              </div>
              <ul className="space-y-0.5">
                {headings.map((h) => (
                  <li key={h.id} className={h.level === 3 ? "pl-3" : ""}>
                    <a
                      href={`#${h.id}`}
                      onClick={() => setOpen(false)}
                      className={cn(
                        "block rounded-md px-2 py-1.5 text-sm transition-colors",
                        activeID === h.id
                          ? "font-medium text-primary"
                          : "text-muted-foreground hover:bg-accent hover:text-foreground",
                      )}
                    >
                      {h.text}
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// Global OS switcher — a segmented control bound to the shared OS context. One
// click re-renders every OS tab block across the page to the chosen platform.
function OSSwitcher({ className }: { className?: string }) {
  const { os, setOS } = useOS();
  const { i18n } = useTranslation();
  const label = (i18n.resolvedLanguage || i18n.language || "en").toLowerCase().startsWith("zh")
    ? "系统"
    : "System";
  return (
    <div className={cn("inline-flex items-center gap-1", className)}>
      <span className="mr-1 hidden text-xs text-muted-foreground sm:inline">{label}</span>
      <div className="inline-flex rounded-lg border border-border bg-card p-0.5">
        {OS_VALUES.map((o) => (
          <button
            key={o}
            type="button"
            onClick={() => setOS(o)}
            aria-pressed={os === o}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-medium transition-colors",
              os === o
                ? "bg-primary text-primary-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {OS_ICONS[o] && <span aria-hidden>{OS_ICONS[o]}</span>}
            {o}
          </button>
        ))}
      </div>
    </div>
  );
}

function collectHeadings(md: string): Array<{ id: string; text: string; level: 2 | 3 }> {
  const out: Array<{ id: string; text: string; level: 2 | 3 }> = [];
  let inFence = false;
  for (const raw of md.split(/\r?\n/)) {
    const line = raw.trimStart();
    if (line.startsWith("```")) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    const m = /^(##|###)\s+(.+?)\s*$/.exec(line);
    if (!m) continue;
    const level = m[1] === "###" ? 3 : 2;
    const text = m[2]?.replace(/`/g, "");
    out.push({ id: slugify(text), text, level });
  }
  return out;
}

// Components passed to react-markdown to override default tags.
const mdComponents = {
  h1: ({ children, ...p }: JSX.IntrinsicElements["h1"] & ExtraProps) => (
    <h1 className="font-display text-3xl font-semibold tracking-tight" {...p}>
      {children}
    </h1>
  ),
  h2: ({ children }: JSX.IntrinsicElements["h2"] & ExtraProps) => {
    const id = slugify(stringifyChildren(children as ReactNode));
    return (
      <h2
        id={id}
        className="group mt-10 flex items-center gap-2 scroll-mt-24 font-display text-2xl font-semibold tracking-tight md:text-3xl"
      >
        <span>{children}</span>
        <a
          href={`#${id}`}
          className="opacity-0 transition-opacity group-hover:opacity-100"
          aria-label="anchor"
        >
          <Hash className="h-4 w-4 text-muted-foreground" />
        </a>
      </h2>
    );
  },
  h3: ({ children }: JSX.IntrinsicElements["h3"] & ExtraProps) => {
    const id = slugify(stringifyChildren(children as ReactNode));
    return (
      <h3
        id={id}
        className="group mt-6 flex items-center gap-2 scroll-mt-24 font-display text-xl font-medium tracking-tight"
      >
        <span>{children}</span>
        <a
          href={`#${id}`}
          className="opacity-0 transition-opacity group-hover:opacity-100"
          aria-label="anchor"
        >
          <Hash className="h-4 w-4 text-muted-foreground" />
        </a>
      </h3>
    );
  },
  p: ({ children }: JSX.IntrinsicElements["p"] & ExtraProps) => (
    <p className="mt-4 text-base leading-relaxed text-foreground/85">{children}</p>
  ),
  ul: ({ children }: JSX.IntrinsicElements["ul"] & ExtraProps) => (
    <ul className="mt-4 space-y-2 text-base text-foreground/85">{children}</ul>
  ),
  ol: ({ children }: JSX.IntrinsicElements["ol"] & ExtraProps) => (
    <ol className="mt-4 list-decimal space-y-2 pl-6 text-base text-foreground/85">{children}</ol>
  ),
  li: ({ children }: JSX.IntrinsicElements["li"] & ExtraProps) => (
    <li className="flex items-start gap-2">
      <span className="mt-2 inline-block h-1.5 w-1.5 flex-shrink-0 rounded-full bg-primary" />
      <span className="min-w-0 flex-1">{children}</span>
    </li>
  ),
  a: ({ children, href }: JSX.IntrinsicElements["a"] & ExtraProps) => {
    const internal = href?.startsWith("/");
    const cls = "text-primary underline-offset-4 hover:underline";
    return internal && href ? (
      <Link to={href} className={cls}>
        {children}
      </Link>
    ) : (
      <a href={href} target="_blank" rel="noreferrer" className={cls}>
        {children}
      </a>
    );
  },
  strong: ({ children }: JSX.IntrinsicElements["strong"] & ExtraProps) => (
    <strong className="font-semibold text-foreground">{children}</strong>
  ),
  blockquote: ({ children }: JSX.IntrinsicElements["blockquote"] & ExtraProps) => (
    <div className="mt-4 rounded-lg border border-primary/30 bg-primary/5 p-5 text-foreground/85 [&>p]:mt-0 [&>p+p]:mt-2">
      {children}
    </div>
  ),
  table: ({ children }: JSX.IntrinsicElements["table"] & ExtraProps) => (
    // overflow-x-auto (not hidden) so wide tables — e.g. the client/Base-URL
    // matrix — scroll on phones instead of clipping the rightmost column.
    <div className="mt-4 overflow-x-auto rounded-lg border border-border">
      <table className="w-full min-w-[32rem] text-sm">{children}</table>
    </div>
  ),
  thead: ({ children }: JSX.IntrinsicElements["thead"] & ExtraProps) => (
    <thead className="bg-muted/40">{children}</thead>
  ),
  tr: ({ children }: JSX.IntrinsicElements["tr"] & ExtraProps) => (
    <tr className="border-b border-border last:border-0 hover:bg-accent/40">{children}</tr>
  ),
  th: ({ children }: JSX.IntrinsicElements["th"] & ExtraProps) => (
    <th className="px-4 py-2.5 text-left font-medium text-foreground">{children}</th>
  ),
  td: ({ children }: JSX.IntrinsicElements["td"] & ExtraProps) => (
    <td className="px-4 py-2.5 align-top text-foreground/85">{children}</td>
  ),
  // Inline vs block code: react-markdown wraps block code in <pre><code>, so
  // here we only need to style inline. Block <pre> is overridden below.
  code: ({
    inline,
    className,
    children,
    ...props
  }: JSX.IntrinsicElements["code"] & ExtraProps & { inline?: boolean }) => {
    const isBlock = !inline && /\blanguage-/.test(className || "");
    if (isBlock)
      return (
        <code className={className} {...props}>
          {children}
        </code>
      );
    return (
      <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.875em] text-primary">
        {children}
      </code>
    );
  },
  pre: ({ children }: JSX.IntrinsicElements["pre"] & ExtraProps) => (
    <CodeBlockShell>{children}</CodeBlockShell>
  ),
  // Screenshots: framed like a real console capture, with the alt text shown as
  // a caption underneath. Uses block-displayed spans (not <figure>) because
  // react-markdown wraps standalone images in a <p>, and <figure> inside <p>
  // is invalid nesting. Lazy-loaded; click to open full size in a new tab.
  img: ({ src, alt }: JSX.IntrinsicElements["img"] & ExtraProps) => (
    <span className="mt-5 block">
      <a
        href={src}
        target="_blank"
        rel="noreferrer"
        className="block overflow-hidden rounded-xl border border-border-strong shadow-lg"
      >
        <img src={src} alt={alt || ""} loading="lazy" className="block w-full" />
      </a>
      {alt && <span className="mt-2 block text-center text-xs text-muted-foreground">{alt}</span>}
    </span>
  ),
  hr: () => <hr className="my-8 border-border" />,
  div: ({ children, ...props }: JSX.IntrinsicElements["div"] & ExtraProps) => {
    const isTabsRoot = "data-tabs" in props;
    const isTab = "data-tab" in props;
    if (isTabsRoot) return <MdTabs>{children as ReactNode}</MdTabs>;
    if (isTab)
      return (
        <MdTabPanel label={(props as Record<string, string>)["data-tab"]}>
          {children as ReactNode}
        </MdTabPanel>
      );
    return <div {...props}>{children}</div>;
  },
};

function stringifyChildren(c: ReactNode): string {
  if (c == null) return "";
  if (typeof c === "string") return c;
  if (typeof c === "number") return String(c);
  if (Array.isArray(c)) return c.map(stringifyChildren).join("");
  if (typeof c === "object" && React.isValidElement(c))
    return stringifyChildren((c as React.ReactElement<{ children?: ReactNode }>).props.children);
  return "";
}

function CodeBlockShell({ children }: { children: ReactNode }) {
  const [copied, setCopied] = useState(false);
  const codeEl = Array.isArray(children) ? children[0] : children;
  const className: string = React.isValidElement(codeEl)
    ? ((codeEl as React.ReactElement<{ className?: string }>).props.className ?? "")
    : "";
  const langMatch = /\blanguage-([\w-]+)/.exec(className);
  const lang = langMatch ? langMatch[1] : "";
  const text = stringifyChildren(
    React.isValidElement(codeEl)
      ? (codeEl as React.ReactElement<{ children?: ReactNode }>).props.children
      : undefined,
  );
  return (
    <div className="group relative mt-4 overflow-hidden rounded-lg border border-border-strong bg-card">
      <div className="flex items-center justify-between border-b border-border bg-muted/30 px-4 py-2">
        <span className="font-mono text-xs uppercase tracking-wider text-muted-foreground">
          {lang || "code"}
        </span>
        <button
          type="button"
          onClick={async () => {
            await copyToClipboard(text);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          }}
          className="inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          {copied ? <Check className="h-3 w-3 text-success" /> : <Copy className="h-3 w-3" />}
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre className="overflow-x-auto px-4 py-4 font-mono text-sm leading-relaxed">{children}</pre>
    </div>
  );
}

// MdTabPanel is just a data container; MdTabs reads its children.
function MdTabPanel({ children }: { label: string; children: ReactNode }) {
  return <>{children}</>;
}

// MdTabs renders a tab bar from child MdTabPanel elements. When the panel
// labels are OS names (macOS/Windows/Linux) the active tab is driven by the
// global OS context instead of local state, so every OS block on the page
// switches together. Non-OS tab groups (e.g. "Permanent" / "Temporary") keep
// independent local state.
function MdTabs({ children }: { children: ReactNode }) {
  const panels = useMemo(() => {
    const arr: Array<{ label: string; content: ReactNode }> = [];
    const visit = (c: ReactNode) => {
      if (!c) return;
      if (Array.isArray(c)) {
        c.forEach(visit);
        return;
      }
      if (React.isValidElement(c)) {
        const el = c as React.ReactElement<{ "data-tab"?: string; children?: ReactNode }>;
        if (el.props && "data-tab" in el.props) {
          arr.push({ label: el.props["data-tab"] ?? "", content: el.props.children });
          return;
        }
        if (el.props?.children) visit(el.props.children);
      }
    };
    visit(children);
    return arr;
  }, [children]);

  const labels = panels.map((p) => p.label);
  const osControlled = isOSGroup(labels);
  const { os, setOS } = useOS();
  const [localActive, setLocalActive] = useState(0);

  if (!panels.length) return null;

  // For OS groups, resolve the active panel from the global OS (falling back to
  // the first panel if this particular block doesn't include the chosen OS).
  let active = localActive;
  if (osControlled) {
    const idx = panels.findIndex((p) => p.label === os);
    active = idx >= 0 ? idx : 0;
  }

  const onTab = (i: number) => {
    if (osControlled) setOS(panels[i]?.label as OS);
    else setLocalActive(i);
  };

  return (
    <div className="mt-4 overflow-hidden rounded-lg border border-border">
      <div className="flex border-b border-border bg-muted/30">
        {panels.map((p, i) => (
          <button
            key={p.label}
            type="button"
            onClick={() => onTab(i)}
            className={cn(
              "px-4 py-2 text-sm font-medium transition-colors",
              active === i
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {p.label}
          </button>
        ))}
      </div>
      <div className="p-4 [&>*:first-child]:mt-0">{panels[active]?.content}</div>
    </div>
  );
}

// Index landing for /docs (no slug). Re-uses the layout but lands on
// /docs/<first-slug> by client-side redirect.
export function DocsIndex() {
  const nav = useNavigate();
  const loc = useLocation();
  const { i18n } = useTranslation();
  useEffect(() => {
    if (loc.pathname === "/docs" || loc.pathname === "/docs/") {
      const docs = docsFor(i18n.resolvedLanguage || i18n.language);
      nav(`/docs/${docs[0]?.slug ?? "quick-start"}`, { replace: true });
    }
  }, [loc.pathname, i18n.resolvedLanguage, i18n.language, nav]);
  return null;
}

export type { DocSection };
