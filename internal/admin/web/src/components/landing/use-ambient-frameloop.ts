import { useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";

/** useAmbientFrameloop gates a decorative R3F scene's render loop.
 *
 * R3F defaults to frameloop="always", so an ambient canvas holds a rAF loop
 * open for as long as its route is mounted — including while scrolled past it
 * or with the tab in the background. "demand" freezes the loop while leaving
 * the last drawn frame on screen, so an idle backdrop looks identical and
 * costs nothing. Reduced motion pins it to a single still frame.
 *
 * Attach `ref` to a wrapper element around the <Canvas> and pass `frameloop`
 * straight through.
 */
export function useAmbientFrameloop() {
  const reduce = useReducedMotion();
  const ref = useRef<HTMLDivElement>(null);
  const [onScreen, setOnScreen] = useState(false);
  const [tabVisible, setTabVisible] = useState(true);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const io = new IntersectionObserver(
      ([entry]) => setOnScreen(entry.isIntersecting),
      // Start a little before it scrolls into view so it is already drifting
      // by the time the user sees it.
      { rootMargin: "120px" },
    );
    io.observe(el);
    const onVis = () => setTabVisible(document.visibilityState === "visible");
    onVis();
    document.addEventListener("visibilitychange", onVis);
    return () => {
      io.disconnect();
      document.removeEventListener("visibilitychange", onVis);
    };
  }, []);

  const frameloop: "always" | "demand" = onScreen && tabVisible && !reduce ? "always" : "demand";
  return { ref, frameloop };
}
