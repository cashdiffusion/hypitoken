import { useId } from "react";

/**
 * HypiToken brand mark — "Confluence": three credential streams converging
 * into one node, emitting a single endpoint line. Deliberately theme-stable
 * (fixed #04110c tile + brand green), unlike --primary which swaps hue
 * between light and dark modes.
 */
export function LogoMark({ className = "h-7 w-7" }: { className?: string }) {
  const grad = useId();
  return (
    <svg viewBox="0 0 64 64" className={className} aria-hidden="true" focusable="false">
      <defs>
        <linearGradient id={grad} x1="0" y1="0" x2="1" y2="0">
          <stop offset="0" stopColor="#38bdf8" />
          <stop offset="0.55" stopColor="#34d399" />
          <stop offset="1" stopColor="#6ee7b7" />
        </linearGradient>
      </defs>
      <rect
        x="0.5"
        y="0.5"
        width="63"
        height="63"
        rx="14"
        fill="#04110c"
        stroke="#34d399"
        strokeOpacity="0.25"
      />
      <g fill="none" stroke={`url(#${grad})`} strokeLinecap="round">
        <path d="M12 17 C23 17 21 32 28.5 32" strokeWidth="4" />
        <path d="M12 32 H28.5" strokeWidth="4" />
        <path d="M12 47 C23 47 21 32 28.5 32" strokeWidth="4" />
        <path d="M39.5 32 H52" strokeWidth="5.5" />
      </g>
      <circle cx="33.5" cy="32" r="5" fill="#34d399" />
    </svg>
  );
}
