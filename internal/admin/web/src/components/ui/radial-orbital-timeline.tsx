import { ArrowRight, Link as LinkIcon, Zap } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export interface TimelineItem {
  id: number;
  title: string;
  /** Small kicker shown in the expanded card header (e.g. a tier or date). */
  date: string;
  content: string;
  /** Short tag rendered as the colored badge. */
  category: string;
  icon: React.ElementType;
  relatedIds: number[];
  status: "completed" | "in-progress" | "pending";
  /** 0–100, drives the metric bar in the expanded card. */
  energy: number;
}

interface RadialOrbitalTimelineProps {
  timelineData: TimelineItem[];
  /** Extra classes for the outer container (height, etc.). */
  className?: string;
  /** Auto-rotate the orbit. Pass false to honor reduced-motion. */
  autoRotate?: boolean;
  /** Translatable labels for the expanded card. */
  labels?: { metric?: string; related?: string };
}

// Theme-aware radial orbital timeline. All colors resolve to the app's CSS
// design tokens (primary / card / border / muted-foreground …) so it adapts
// to both light and dark palettes. Status only drives the accent color; the
// badge displays the node's `category`.
export default function RadialOrbitalTimeline({
  timelineData,
  className = "",
  autoRotate = true,
  labels,
}: RadialOrbitalTimelineProps) {
  const metricLabel = labels?.metric ?? "Energy";
  const relatedLabel = labels?.related ?? "Connected nodes";

  const [expandedItems, setExpandedItems] = useState<Record<number, boolean>>({});
  const [rotationAngle, setRotationAngle] = useState<number>(0);
  const [spinning, setSpinning] = useState<boolean>(autoRotate);
  const [pulseEffect, setPulseEffect] = useState<Record<number, boolean>>({});
  const [activeNodeId, setActiveNodeId] = useState<number | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const orbitRef = useRef<HTMLDivElement>(null);
  const nodeRefs = useRef<Record<number, HTMLButtonElement | null>>({});

  // Re-sync with the parent's autoRotate (e.g. reduced-motion toggles).
  useEffect(() => {
    setSpinning(autoRotate);
  }, [autoRotate]);

  const getRelatedItems = (itemId: number): number[] => {
    const currentItem = timelineData.find((item) => item.id === itemId);
    return currentItem ? currentItem.relatedIds : [];
  };

  const centerViewOnNode = (nodeId: number) => {
    if (!nodeRefs.current[nodeId]) return;
    const nodeIndex = timelineData.findIndex((item) => item.id === nodeId);
    const targetAngle = (nodeIndex / timelineData.length) * 360;
    setRotationAngle(270 - targetAngle);
  };

  const handleContainerClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if (e.target === containerRef.current || e.target === orbitRef.current) {
      setExpandedItems({});
      setActiveNodeId(null);
      setPulseEffect({});
      setSpinning(autoRotate);
    }
  };

  const toggleItem = (id: number) => {
    setExpandedItems((prev) => {
      const newState = { ...prev };
      Object.keys(newState).forEach((key) => {
        if (parseInt(key, 10) !== id) newState[parseInt(key, 10)] = false;
      });

      newState[id] = !prev[id];

      if (!prev[id]) {
        setActiveNodeId(id);
        setSpinning(false);

        const newPulseEffect: Record<number, boolean> = {};
        getRelatedItems(id).forEach((relId) => {
          newPulseEffect[relId] = true;
        });
        setPulseEffect(newPulseEffect);

        centerViewOnNode(id);
      } else {
        setActiveNodeId(null);
        setSpinning(autoRotate);
        setPulseEffect({});
      }

      return newState;
    });
  };

  useEffect(() => {
    let rotationTimer: ReturnType<typeof setInterval> | undefined;
    if (spinning) {
      rotationTimer = setInterval(() => {
        setRotationAngle((prev) => Number(((prev + 0.3) % 360).toFixed(3)));
      }, 50);
    }
    return () => {
      if (rotationTimer) clearInterval(rotationTimer);
    };
  }, [spinning]);

  const calculateNodePosition = (index: number, total: number) => {
    const angle = ((index / total) * 360 + rotationAngle) % 360;
    const radius = 200;
    const radian = (angle * Math.PI) / 180;

    const x = radius * Math.cos(radian);
    const y = radius * Math.sin(radian);

    const zIndex = Math.round(100 + 50 * Math.cos(radian));
    const opacity = Math.max(0.4, Math.min(1, 0.4 + 0.6 * ((1 + Math.sin(radian)) / 2)));

    return { x, y, angle, zIndex, opacity };
  };

  const isRelatedToActive = (itemId: number): boolean => {
    if (!activeNodeId) return false;
    return getRelatedItems(activeNodeId).includes(itemId);
  };

  // Accent token for the node ring / glow, keyed on status.
  const statusAccent = (status: TimelineItem["status"]): string => {
    switch (status) {
      case "completed":
        return "var(--primary)";
      case "in-progress":
        return "var(--info)";
      default:
        return "var(--muted-foreground)";
    }
  };

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: outer backdrop only deselects on click/Escape; screen readers interact with individual orbit nodes
    <div
      className={`relative flex w-full items-center justify-center overflow-hidden ${className}`}
      ref={containerRef}
      onClick={handleContainerClick}
      onKeyDown={(e) => {
        if (e.key === "Escape")
          handleContainerClick(e as unknown as React.MouseEvent<HTMLDivElement>);
      }}
    >
      <div className="relative flex h-full w-full max-w-4xl items-center justify-center">
        <div
          className="absolute flex h-full w-full items-center justify-center"
          ref={orbitRef}
          style={{ perspective: "1000px" }}
        >
          {/* Center pulse — the gateway core */}
          <div className="absolute z-10 flex h-16 w-16 items-center justify-center rounded-full bg-gradient-to-br from-primary via-info to-primary animate-pulse">
            <div className="absolute h-20 w-20 animate-ping rounded-full border border-primary/30 opacity-70" />
            <div
              className="absolute h-24 w-24 animate-ping rounded-full border border-primary/15 opacity-50"
              style={{ animationDelay: "0.5s" }}
            />
            <div className="h-8 w-8 rounded-full bg-background/80 backdrop-blur-md" />
          </div>

          {/* Orbit ring */}
          <div className="absolute h-96 w-96 rounded-full border border-border" />

          {timelineData.map((item, index) => {
            const position = calculateNodePosition(index, timelineData.length);
            const isExpanded = expandedItems[item.id];
            const isRelated = isRelatedToActive(item.id);
            const isPulsing = pulseEffect[item.id];
            const Icon = item.icon;
            const accent = statusAccent(item.status);

            const nodeStyle = {
              transform: `translate(${position.x}px, ${position.y}px)`,
              zIndex: isExpanded ? 200 : position.zIndex,
              opacity: isExpanded ? 1 : position.opacity,
            };

            return (
              <button
                type="button"
                key={item.id}
                ref={(el) => {
                  nodeRefs.current[item.id] = el;
                }}
                className="absolute cursor-pointer transition-all duration-700 border-0 bg-transparent p-0"
                style={nodeStyle}
                onClick={(e) => {
                  e.stopPropagation();
                  toggleItem(item.id);
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.stopPropagation();
                    toggleItem(item.id);
                  }
                }}
              >
                {/* Soft glow scaled by energy */}
                <div
                  className={`absolute rounded-full -inset-1 ${isPulsing ? "animate-pulse duration-1000" : ""}`}
                  style={{
                    background: `radial-gradient(circle, color-mix(in oklch, ${accent} 35%, transparent) 0%, transparent 70%)`,
                    width: `${item.energy * 0.5 + 40}px`,
                    height: `${item.energy * 0.5 + 40}px`,
                    left: `-${(item.energy * 0.5) / 2}px`,
                    top: `-${(item.energy * 0.5) / 2}px`,
                  }}
                />

                <div
                  className={`flex h-10 w-10 items-center justify-center rounded-full border-2 transition-all duration-300 ${
                    isExpanded
                      ? "scale-150 bg-primary text-primary-foreground border-primary shadow-lg shadow-primary/30"
                      : isRelated
                        ? "bg-primary/40 text-primary-foreground border-primary animate-pulse"
                        : "bg-card text-foreground border-border-strong"
                  }`}
                >
                  <Icon size={16} />
                </div>

                <div
                  className={`absolute top-12 whitespace-nowrap text-xs font-semibold tracking-wider transition-all duration-300 ${
                    isExpanded ? "scale-125 text-foreground" : "text-muted-foreground"
                  }`}
                >
                  {item.title}
                </div>

                {isExpanded && (
                  <Card className="absolute left-1/2 top-20 w-64 -translate-x-1/2 overflow-visible border-border bg-card/90 shadow-xl backdrop-blur-lg">
                    <div className="absolute -top-3 left-1/2 h-3 w-px -translate-x-1/2 bg-border-strong" />
                    <CardHeader className="pb-2">
                      <div className="flex items-center justify-between">
                        <Badge
                          variant="outline"
                          className="px-2 text-[10px] uppercase tracking-wider"
                          style={{
                            color: accent,
                            borderColor: `color-mix(in oklch, ${accent} 45%, transparent)`,
                            backgroundColor: `color-mix(in oklch, ${accent} 12%, transparent)`,
                          }}
                        >
                          {item.category}
                        </Badge>
                        <span className="font-mono text-xs text-muted-foreground">{item.date}</span>
                      </div>
                      <CardTitle className="mt-2 text-sm text-foreground">{item.title}</CardTitle>
                    </CardHeader>
                    <CardContent className="text-xs text-muted-foreground">
                      <p className="leading-relaxed">{item.content}</p>

                      <div className="mt-4 border-t border-border pt-3">
                        <div className="mb-1 flex items-center justify-between text-xs">
                          <span className="flex items-center text-muted-foreground">
                            <Zap size={10} className="mr-1" />
                            {metricLabel}
                          </span>
                          <span className="font-mono text-foreground/80">{item.energy}%</span>
                        </div>
                        <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
                          <div
                            className="h-full rounded-full bg-gradient-to-r from-primary to-info"
                            style={{ width: `${item.energy}%` }}
                          />
                        </div>
                      </div>

                      {item.relatedIds.length > 0 && (
                        <div className="mt-4 border-t border-border pt-3">
                          <div className="mb-2 flex items-center">
                            <LinkIcon size={10} className="mr-1 text-muted-foreground" />
                            <h4 className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                              {relatedLabel}
                            </h4>
                          </div>
                          <div className="flex flex-wrap gap-1">
                            {item.relatedIds.map((relatedId) => {
                              const relatedItem = timelineData.find((i) => i.id === relatedId);
                              return (
                                <Button
                                  key={relatedId}
                                  variant="outline"
                                  size="sm"
                                  className="flex h-6 items-center px-2 py-0 text-xs text-muted-foreground transition-all hover:text-foreground"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    toggleItem(relatedId);
                                  }}
                                >
                                  {relatedItem?.title}
                                  <ArrowRight size={8} className="ml-1 opacity-60" />
                                </Button>
                              );
                            })}
                          </div>
                        </div>
                      )}
                    </CardContent>
                  </Card>
                )}
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
