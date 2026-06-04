// Global "current OS" context for the docs. A single selection (macOS /
// Windows / Linux) drives every OS-specific tab block across all doc pages at
// once, so the reader picks their platform a single time. The default is
// detected from the browser UA; the choice is then persisted to localStorage.

import { createContext, type ReactNode, useContext, useEffect, useState } from "react";

export type OS = "macOS" | "Windows" | "Linux";

export const OS_VALUES: OS[] = ["macOS", "Windows", "Linux"];

const STORAGE_KEY = "hypi-docs-os";

// A tab group is treated as OS-controlled when its labels are a subset of the
// OS names (and it has at least two of them). Callers pass the panel labels.
export function isOSGroup(labels: string[]): boolean {
  if (labels.length < 2) return false;
  return labels.every((l) => (OS_VALUES as string[]).includes(l));
}

// detectOS guesses the visitor's platform. Prefers the modern
// navigator.userAgentData.platform (not subject to UA freezing), falls back to
// navigator.platform / userAgent. Mobile and unknown platforms default to
// macOS — the most common dev laptop and a safe Unix-style default.
export function detectOS(): OS {
  if (typeof navigator === "undefined") return "macOS";
  const uaData = (navigator as unknown as { userAgentData?: { platform?: string } }).userAgentData;
  const hint =
    `${uaData?.platform || ""} ${navigator.platform || ""} ${navigator.userAgent || ""}`.toLowerCase();
  if (/win/.test(hint)) return "Windows";
  if (/mac/.test(hint)) return "macOS";
  if (/linux|x11|ubuntu|debian|fedora|cros/.test(hint)) return "Linux";
  return "macOS";
}

interface OSContextValue {
  os: OS;
  setOS: (os: OS) => void;
}

const OSContext = createContext<OSContextValue>({ os: "macOS", setOS: () => {} });

export function OSProvider({ children }: { children: ReactNode }) {
  const [os, setOSState] = useState<OS>(() => {
    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved && (OS_VALUES as string[]).includes(saved)) return saved as OS;
    } catch {
      /* localStorage unavailable (private mode); fall through to detection */
    }
    return detectOS();
  });

  const setOS = (next: OS) => {
    setOSState(next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      /* ignore persistence failures */
    }
  };

  // Keep multiple open tabs / windows in sync when the choice changes.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === STORAGE_KEY && e.newValue && (OS_VALUES as string[]).includes(e.newValue)) {
        setOSState(e.newValue as OS);
      }
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  return <OSContext.Provider value={{ os, setOS }}>{children}</OSContext.Provider>;
}

export function useOS(): OSContextValue {
  return useContext(OSContext);
}
