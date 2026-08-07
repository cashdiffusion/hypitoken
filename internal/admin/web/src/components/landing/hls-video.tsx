import { type CSSProperties, useEffect, useRef } from "react";
import { cn } from "@/lib/utils";
import { usePrefersReducedMotion } from "./use-media";

interface HlsVideoProps {
  /** A `.m3u8` (HLS) or `.mp4` source. HLS is attached via hls.js where the
   *  browser can't play it natively (everything except Safari). */
  src: string;
  poster?: string;
  className?: string;
  style?: CSSProperties;
  /** Solid fallback shown before first frame / when motion is reduced. */
  fallbackColor?: string;
}

// Background video that transparently handles HLS. Always muted + looping +
// inline so mobile browsers allow autoplay. When the user prefers reduced
// motion we render a static gradient instead of a moving video.
export function HlsVideo({
  src,
  poster,
  className,
  style,
  fallbackColor = "#05140f",
}: HlsVideoProps) {
  const ref = useRef<HTMLVideoElement>(null);
  const reduced = usePrefersReducedMotion();

  useEffect(() => {
    const video = ref.current;
    if (!video || reduced) return;
    const isHls = src.endsWith(".m3u8");
    const nativeHls = video.canPlayType("application/vnd.apple.mpegurl");

    const tryPlay = () => video.play().catch(() => {});
    video.addEventListener("loadeddata", tryPlay);

    // hls.js is 162 kB gzipped and is only needed for an .m3u8 source on a
    // browser without native HLS. Importing it statically put all of it on the
    // landing page's critical path even though the hero is a plain .mp4, so it
    // is pulled in on demand and the mp4 path never touches it.
    let destroy: (() => void) | undefined;
    let cancelled = false;
    if (isHls && !nativeHls) {
      void import("hls.js").then(({ default: Hls }) => {
        if (cancelled || !Hls.isSupported()) return;
        const hls = new Hls({
          enableWorker: true,
          lowLatencyMode: false,
          capLevelToPlayerSize: true,
        });
        hls.loadSource(src);
        hls.attachMedia(video);
        destroy = () => hls.destroy();
        tryPlay();
      });
    } else {
      video.src = src;
      tryPlay();
    }

    return () => {
      cancelled = true;
      video.removeEventListener("loadeddata", tryPlay);
      destroy?.();
    };
  }, [src, reduced]);

  if (reduced) {
    return (
      <div
        aria-hidden
        className={cn("absolute inset-0", className)}
        style={{
          background: `radial-gradient(ellipse 80% 60% at 50% 0%, color-mix(in oklch, var(--primary) 25%, ${fallbackColor}) 0%, ${fallbackColor} 70%)`,
          ...style,
        }}
      />
    );
  }

  return (
    <video
      ref={ref}
      poster={poster}
      muted
      loop
      playsInline
      autoPlay
      preload="auto"
      aria-hidden
      className={cn("absolute inset-0 h-full w-full object-cover", className)}
      style={{ backgroundColor: fallbackColor, ...style }}
    />
  );
}
