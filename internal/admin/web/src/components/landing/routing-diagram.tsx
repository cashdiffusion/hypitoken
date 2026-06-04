import { useEffect, useRef } from "react";
import gsap from "gsap";
import { MotionPathPlugin } from "gsap/MotionPathPlugin";
import { useTranslation } from "react-i18next";

gsap.registerPlugin(MotionPathPlugin);

interface Node {
  label: string;
  sub?: string;
  tone: "primary" | "info" | "muted" | "success";
}

const NODE_W = 132;
const NODE_H = 48;
const CLIENT_X = 16;
const CRED_X = 372;
const ROWS = [62, 160, 258];

const toneColor: Record<Node["tone"], string> = {
  primary: "var(--color-primary)",
  info: "var(--color-info)",
  muted: "var(--color-muted-foreground)",
  success: "var(--color-success)",
};

// Live routing visual: client surfaces → gateway → credential pool. GSAP runs
// packets along the connector paths (inbound, then outbound) so the diagram
// reads as traffic being scheduled across the pool in real time. The middle
// credential is "sticky" and keeps a steady pulse. Animation is skipped under
// reduced-motion (the static diagram still communicates the topology).
export function RoutingDiagram() {
  const { t } = useTranslation();
  const ref = useRef<SVGSVGElement>(null);

  const clients: Node[] = [
    { label: t("home.archNodes.ccLabel"), sub: t("home.archNodes.ccSub"), tone: "primary" },
    { label: t("home.archNodes.codexLabel"), sub: t("home.archNodes.codexSub"), tone: "info" },
    { label: t("home.archNodes.sdkLabel"), sub: t("home.archNodes.sdkSub"), tone: "muted" },
  ];
  const creds = (t("home.archCreds", { returnObjects: true }) as unknown as string[]) || [];

  const inPaths = ROWS.map((y) => `M${CLIENT_X + NODE_W},${y} C190,${y} 196,160 224,160`);
  const outPaths = ROWS.map((y) => `M296,160 C324,160 330,${y} ${CRED_X},${y}`);

  useEffect(() => {
    const svg = ref.current;
    if (!svg) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    const ctx = gsap.context(() => {
      const run = (sel: string, paths: string[], baseDelay: number) =>
        paths.forEach((d, i) => {
          gsap.set(`${sel}-${i}`, { opacity: 0 });
          gsap.to(`${sel}-${i}`, {
            duration: 1.4,
            repeat: -1,
            ease: "power1.inOut",
            delay: baseDelay + i * 0.45,
            repeatDelay: 0.8,
            keyframes: { opacity: [0, 1, 1, 0] },
            motionPath: { path: d, alignOrigin: [0.5, 0.5] },
          });
        });
      run(".pkt-in", inPaths, 0);
      run(".pkt-out", outPaths, 0.7);

      gsap.to(".sticky-pulse", {
        scale: 1.18,
        opacity: 0.35,
        transformOrigin: "center",
        duration: 1.3,
        repeat: -1,
        yoyo: true,
        ease: "sine.inOut",
      });
    }, svg);
    return () => ctx.revert();
  }, [inPaths.join(), outPaths.join()]);

  return (
    <svg
      ref={ref}
      viewBox="0 0 520 320"
      className="w-full"
      role="img"
      aria-label={t("home.archTitle")}
    >
      {/* connectors */}
      <g fill="none" strokeWidth="1.5">
        {[...inPaths, ...outPaths].map((d, i) => (
          <path key={i} d={d} stroke="var(--color-border-strong)" strokeOpacity="0.7" />
        ))}
      </g>

      {/* packets */}
      {ROWS.map((_, i) => (
        <circle key={`in-${i}`} className={`pkt-in-${i}`} r="3.2" fill="var(--color-primary)" />
      ))}
      {ROWS.map((_, i) => (
        <circle key={`out-${i}`} className={`pkt-out-${i}`} r="3.2" fill="var(--color-success)" />
      ))}

      {/* client nodes */}
      {clients.map((n, i) => (
        <NodeBox key={`c-${i}`} x={CLIENT_X} y={ROWS[i] - NODE_H / 2} node={n} />
      ))}

      {/* gateway */}
      <g>
        <rect x={224} y={120} width={72} height={80} rx={14} fill="color-mix(in oklch, var(--color-primary) 12%, var(--color-card))" stroke="color-mix(in oklch, var(--color-primary) 45%, transparent)" strokeWidth="1.5" />
        <text x={260} y={156} textAnchor="middle" className="fill-[var(--color-primary)] font-mono" fontSize="12" fontWeight="600">GW</text>
        <text x={260} y={172} textAnchor="middle" className="fill-[var(--color-muted-foreground)]" fontSize="8">pool</text>
      </g>

      {/* credential nodes */}
      {creds.slice(0, 3).map((label, i) => (
        <g key={`cr-${i}`}>
          {i === 1 && (
            <circle className="sticky-pulse" cx={CRED_X + 14} cy={ROWS[i]} r="9" fill="var(--color-success)" opacity="0.5" />
          )}
          <NodeBox
            x={CRED_X}
            y={ROWS[i] - NODE_H / 2}
            node={{ label, tone: i === 2 ? "muted" : "success", sub: i === 1 ? t("home.archStickyTag") : undefined }}
            dot
          />
        </g>
      ))}
    </svg>
  );
}

function NodeBox({ x, y, node, dot }: { x: number; y: number; node: Node; dot?: boolean }) {
  return (
    <g>
      <rect
        x={x}
        y={y}
        width={NODE_W}
        height={NODE_H}
        rx={11}
        fill="color-mix(in oklch, var(--color-card) 88%, transparent)"
        stroke="var(--color-border)"
        strokeWidth="1"
      />
      {dot && <circle cx={x + 14} cy={y + NODE_H / 2} r="3" fill={toneColor[node.tone]} />}
      <text
        x={dot ? x + 26 : x + 14}
        y={node.sub ? y + 21 : y + 28}
        className="font-mono"
        fontSize="12.5"
        fontWeight="600"
        fill={toneColor[node.tone]}
      >
        {node.label}
      </text>
      {node.sub && (
        <text x={dot ? x + 26 : x + 14} y={y + 36} fontSize="9" fill="var(--color-muted-foreground)">
          {node.sub}
        </text>
      )}
    </g>
  );
}
