import { useEffect, useState, type ReactNode, type ElementType } from "react";
import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";
import { animate, useReducedMotion } from "motion/react";
import { Reveal } from "@/components/landing/reveal";
import { SpotlightCard } from "@/components/landing/interactions";
import { cn } from "@/lib/utils";

const EASE = [0.22, 1, 0.36, 1] as const;

/* PageHeader — the consistent top-of-page block for every signed-in route:
 * a green eyebrow, a display-weight title (optionally icon-led), an optional
 * subtitle, and a right-aligned action slot. Reveal-wrapped so it eases in. */
export function PageHeader({
  eyebrow, title, sub, action, icon: Icon,
}: {
  eyebrow?: string;
  title: ReactNode;
  sub?: ReactNode;
  action?: ReactNode;
  icon?: ElementType;
}) {
  return (
    <Reveal>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0">
          {eyebrow && <span className="eyebrow text-primary">{eyebrow}</span>}
          <h1 className="mt-2 flex items-center gap-2.5 font-display text-3xl font-semibold tracking-tight md:text-4xl">
            {Icon && <Icon className="h-7 w-7 text-primary" />}
            {title}
          </h1>
          {sub && <p className="mt-1.5 text-muted-foreground">{sub}</p>}
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </div>
    </Reveal>
  );
}

/* CountUp — eases a number from 0 to its value on mount/change. Honours
 * reduced motion (snaps straight to the value). `format` lets callers render
 * USD, integers, etc. */
export function CountUp({
  value, format = (n) => n.toFixed(0), durationMs = 900,
}: {
  value: number;
  format?: (n: number) => string;
  durationMs?: number;
}) {
  const reduce = useReducedMotion();
  const [display, setDisplay] = useState(value);
  useEffect(() => {
    if (reduce) { setDisplay(value); return; }
    const controls = animate(0, value, {
      duration: durationMs / 1000,
      ease: EASE,
      onUpdate: (v) => setDisplay(v),
    });
    return () => controls.stop();
  }, [value, reduce, durationMs]);
  return <span className="tabular-nums">{format(display)}</span>;
}

/* StatTile — a glass SpotlightCard KPI tile: small uppercase label + icon,
 * a large mono value, an optional sub-line, and an optional ghost CTA. */
export function StatTile({
  icon: Icon, label, value, sub, accent, cta,
}: {
  icon: ElementType;
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  accent?: boolean;
  cta?: { to: string; label: string };
}) {
  return (
    <SpotlightCard className={cn("h-full w-full", accent && "ring-1 ring-primary/30")}>
      <div className="flex items-center justify-between">
        <span className="text-xs uppercase tracking-wider text-muted-foreground">{label}</span>
        <span className={cn(
          "grid h-8 w-8 place-items-center rounded-lg",
          accent ? "bg-primary/15 text-primary" : "bg-muted/60 text-muted-foreground"
        )}>
          <Icon className="h-4 w-4" />
        </span>
      </div>
      <div className={cn(
        "mt-3 font-mono text-3xl font-semibold tracking-tight tabular-nums",
        accent && "text-primary"
      )}>
        {value}
      </div>
      {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
      {cta && (
        <Link
          to={cta.to}
          className="mt-4 inline-flex items-center gap-1 text-sm font-medium text-primary transition-colors hover:text-primary/80"
        >
          {cta.label} <ArrowRight className="h-3.5 w-3.5" />
        </Link>
      )}
    </SpotlightCard>
  );
}

/* GlassPanel — a plain frosted surface for tables / lists / content blocks.
 * Optional title + description header keeps section semantics consistent. */
export function GlassPanel({
  title, description, action, children, className, bodyClassName,
}: {
  title?: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
}) {
  return (
    <div className={cn("glass overflow-hidden rounded-2xl", className)}>
      {(title || action) && (
        <div className="flex items-start justify-between gap-4 border-b border-border/60 px-5 py-4 md:px-6">
          <div className="min-w-0">
            {title && <h2 className="font-display text-lg font-semibold tracking-tight">{title}</h2>}
            {description && <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>}
          </div>
          {action && <div className="shrink-0">{action}</div>}
        </div>
      )}
      <div className={cn("p-5 md:p-6", bodyClassName)}>{children}</div>
    </div>
  );
}
