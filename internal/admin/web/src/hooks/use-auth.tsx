import { createContext, type ReactNode, useContext, useEffect, useState } from "react";
import { apiGet, getCachedUser, getJWT, logout, setCachedUser, setJWT } from "@/lib/api";
import type { PricingGroup, User } from "@/lib/types";
import { errStatus } from "@/lib/utils";

interface AuthState {
  user: User | null;
  group: PricingGroup | null;
  loading: boolean;
  refresh: () => Promise<void>;
  signIn: (token: string, user: User) => void;
  signOut: () => void;
}

const Ctx = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(getCachedUser());
  const [group, setGroup] = useState<PricingGroup | null>(null);
  const [loading, setLoading] = useState<boolean>(!!getJWT());

  const refresh = async () => {
    if (!getJWT()) {
      setUser(null);
      setGroup(null);
      setLoading(false);
      return;
    }
    try {
      const r = await apiGet<{ user: User; group: PricingGroup }>("/me");
      setUser(r.user);
      setGroup(r.group);
      setCachedUser(r.user);
    } catch (e) {
      if (errStatus(e) === 401) {
        logout();
        setUser(null);
        setGroup(null);
      }
    } finally {
      setLoading(false);
    }
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: mount-only bootstrap; refresh is stable and does not need to re-run on every render
  useEffect(() => {
    refresh();
  }, []);

  const signIn = (token: string, u: User) => {
    setJWT(token);
    setCachedUser(u);
    setUser(u);
    // fire-and-forget — populates group + verifies token
    refresh();
  };

  const signOut = () => {
    logout();
    setUser(null);
    setGroup(null);
  };

  return (
    <Ctx.Provider value={{ user, group, loading, refresh, signIn, signOut }}>
      {children}
    </Ctx.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
