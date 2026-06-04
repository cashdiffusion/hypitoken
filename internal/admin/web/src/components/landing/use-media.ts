import { useEffect, useState } from "react";

// useIsMobile — true when the viewport is below `breakpoint` (default 640px,
// matching Tailwind's `sm`). Updated on resize. Used to scale back the R3F
// ambient layer and other expensive flourishes on phones.
export function useIsMobile(breakpoint = 640): boolean {
  const [isMobile, setIsMobile] = useState(() =>
    typeof window === "undefined" ? false : window.innerWidth < breakpoint,
  );
  useEffect(() => {
    const mq = window.matchMedia(`(max-width: ${breakpoint - 1}px)`);
    const onChange = () => setIsMobile(mq.matches);
    onChange();
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [breakpoint]);
  return isMobile;
}

// usePrefersReducedMotion — respects the OS "reduce motion" setting. When true
// we drop the video autoplay, the particle field, and entrance transforms so
// the page is calm and accessible.
export function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const onChange = () => setReduced(mq.matches);
    onChange();
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return reduced;
}
