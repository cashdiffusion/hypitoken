import type { ElementType, ReactNode } from "react";
import { cn } from "@/lib/utils";

// Adapted from the 21st.dev "features-10" (Tailark) block. Re-themed onto the
// project's OKLCH design tokens (the original used `hsl(var(--…))`) and wired to
// real product visuals instead of placeholder screenshots. Sharp rounded-none
// cards with primary corner brackets; two visual cards on top, one wide
// decorative card below.

interface VisualCard {
  icon: ElementType;
  /** Small label above the headline. */
  label: string;
  /** Prominent headline. */
  title: string;
  /** Optional supporting line. */
  body?: string;
  /** Live demo / diagram rendered in the card body. */
  visual: ReactNode;
}

interface FeatureShowcaseProps {
  cards: [VisualCard, VisualCard];
  wideTitle: string;
  wideLabels: string[];
}

export function FeatureShowcase({ cards, wideTitle, wideLabels }: FeatureShowcaseProps) {
  // Four decorative node clusters for the wide card, themed to design tokens.
  const clusters: CircleConfig[][] = [
    [{ pattern: "border" }, { pattern: "border" }],
    [{ pattern: "none" }, { pattern: "primary" }],
    [{ pattern: "info" }, { pattern: "none" }],
    [{ pattern: "primary" }, { pattern: "none" }],
  ];

  return (
    // grid-cols-1 (not implicit) is required: an implicit single column sizes
    // to the cards' max-content (~527px) and overflows phones; minmax(0,1fr)
    // lets the cards fill and shrink so their visuals scroll within instead.
    <div className="mx-auto grid max-w-6xl grid-cols-1 gap-4 lg:grid-cols-2">
      {cards.map((c) => (
        <FeatureCard key={c.title}>
          <CardHeading icon={c.icon} label={c.label} title={c.title} body={c.body} />
          <div className="relative border-t border-dashed border-border p-4">
            <div
              aria-hidden
              className="pointer-events-none absolute inset-0"
              style={{
                background:
                  "radial-gradient(125% 125% at 50% 0%, transparent 45%, color-mix(in oklch, var(--muted) 45%, transparent) 100%)",
              }}
            />
            <div className="relative">{c.visual}</div>
          </div>
        </FeatureCard>
      ))}

      <FeatureCard className="p-6 lg:col-span-2">
        <p className="mx-auto my-6 max-w-md text-balance text-center font-display text-2xl font-semibold tracking-tight">
          {wideTitle}
        </p>
        <div className="flex justify-center gap-6 overflow-hidden">
          {clusters.map((circles, i) => (
            <CircularUI
              key={circles.map((c) => c.pattern).join("-")}
              label={wideLabels[i] ?? ""}
              circles={circles}
              className={i === 3 ? "hidden sm:block" : undefined}
            />
          ))}
        </div>
      </FeatureCard>
    </div>
  );
}

function FeatureCard({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={cn(
        "group relative rounded-none border border-border bg-card shadow-sm transition-colors hover:border-border-strong",
        className,
      )}
    >
      <CardDecorator />
      {children}
    </div>
  );
}

function CardDecorator() {
  return (
    <>
      <span className="absolute -left-px -top-px block size-2 border-l-2 border-t-2 border-primary" />
      <span className="absolute -right-px -top-px block size-2 border-r-2 border-t-2 border-primary" />
      <span className="absolute -bottom-px -left-px block size-2 border-b-2 border-l-2 border-primary" />
      <span className="absolute -bottom-px -right-px block size-2 border-b-2 border-r-2 border-primary" />
    </>
  );
}

function CardHeading({
  icon: Icon,
  label,
  title,
  body,
}: {
  icon: ElementType;
  label: string;
  title: string;
  body?: string;
}) {
  return (
    <div className="relative p-6">
      <RippleBadge icon={Icon} className="absolute right-6 top-6" />
      <span className="text-sm text-muted-foreground">{label}</span>
      <p className="mt-4 max-w-[85%] font-display text-2xl font-semibold tracking-tight text-foreground">
        {title}
      </p>
      {body && (
        <p className="mt-2 max-w-[92%] text-sm leading-relaxed text-muted-foreground">{body}</p>
      )}
    </div>
  );
}

// Concentric-ripple icon badge — the reference's signature feature-card accent,
// rendered with the project's primary token so it tints with the theme.
function RippleBadge({ icon: Icon, className }: { icon: ElementType; className?: string }) {
  return (
    <span
      className={cn(
        "relative grid size-11 place-items-center rounded-full bg-primary/10 text-primary ring-1 ring-primary/20",
        className,
      )}
    >
      <span
        aria-hidden
        className="absolute inset-0 scale-[1.35] rounded-full ring-1 ring-primary/15"
      />
      <span
        aria-hidden
        className="absolute inset-0 scale-[1.7] rounded-full ring-1 ring-primary/[0.08]"
      />
      <Icon className="size-5" />
    </span>
  );
}

interface CircleConfig {
  pattern: "none" | "border" | "primary" | "info";
}

function CircularUI({
  label,
  circles,
  className,
}: {
  label: string;
  circles: CircleConfig[];
  className?: string;
}) {
  const stripe = (color: string) =>
    `repeating-linear-gradient(-45deg, ${color}, ${color} 1px, transparent 1px, transparent 4px)`;
  return (
    <div className={className}>
      <div className="size-fit rounded-2xl bg-gradient-to-b from-border to-transparent p-px">
        <div className="relative flex aspect-square w-fit items-center -space-x-4 rounded-[15px] bg-gradient-to-b from-background to-muted/25 p-4">
          {circles.map((circle, i) => (
            <div
              // biome-ignore lint/suspicious/noArrayIndexKey: static decorative list — circle positions are purely positional within a fixed-size cluster
              key={i}
              className="size-7 rounded-full border border-primary sm:size-8"
              style={
                circle.pattern === "border"
                  ? { background: stripe("var(--border)") }
                  : circle.pattern === "primary"
                    ? { background: stripe("var(--primary)") }
                    : circle.pattern === "info"
                      ? { borderColor: "var(--info)", background: stripe("var(--info)") }
                      : undefined
              }
            />
          ))}
        </div>
      </div>
      <span className="mt-1.5 block text-center text-sm text-muted-foreground">{label}</span>
    </div>
  );
}
