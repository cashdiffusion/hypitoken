import { Link, NavLink, Outlet, useNavigate } from "react-router-dom";
import { useAuth } from "@/hooks/use-auth";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { LogOut, User as UserIcon, Wallet, KeyRound, LayoutDashboard, Activity, Shield, Home, BookOpen, ExternalLink, Tag } from "lucide-react";
import { fmtUSD } from "@/lib/utils";
import { cn } from "@/lib/utils";

const NAV = [
  { to: "/app", label: "Dashboard", icon: LayoutDashboard, end: true },
  { to: "/app/tokens", label: "Tokens", icon: KeyRound },
  { to: "/app/billing", label: "Billing", icon: Wallet },
];

// Public-site links surfaced inside the authed shell so users can always
// jump back to the marketing / docs / status pages without logging out.
const PUBLIC_NAV = [
  { to: "/", label: "Home", icon: Home, external: false },
  { to: "/pricing", label: "Pricing", icon: Tag, external: false },
  { to: "/docs", label: "Documentation", icon: BookOpen, external: false },
  { to: "/status", label: "System status", icon: Activity, external: false },
];

export function AppShell() {
  const { user, signOut } = useAuth();
  const nav = useNavigate();
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <Header />
      <div className="mx-auto flex max-w-7xl gap-6 px-4 py-6 md:px-6 lg:py-10">
        <aside className="hidden w-56 flex-shrink-0 lg:block">
          <nav className="sticky top-24 flex flex-col gap-1">
            {NAV.map((n) => (
              <NavLink
                key={n.to}
                to={n.to}
                end={n.end}
                className={({ isActive }) =>
                  cn(
                    "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors",
                    isActive
                      ? "bg-primary/10 text-primary font-medium"
                      : "text-muted-foreground hover:bg-accent hover:text-foreground"
                  )
                }
              >
                <n.icon className="h-4 w-4" />
                {n.label}
              </NavLink>
            ))}
            {user?.role === "admin" && (
              <>
                <div className="mt-6 mb-1 px-3 text-xs uppercase tracking-wider text-muted-foreground">Operator</div>
                <NavLink
                  to="/app/admin"
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors",
                      isActive
                        ? "bg-primary/10 text-primary font-medium"
                        : "text-muted-foreground hover:bg-accent hover:text-foreground"
                    )
                  }
                >
                  <Shield className="h-4 w-4" /> Admin
                </NavLink>
              </>
            )}
            <div className="mt-6 mb-1 px-3 text-xs uppercase tracking-wider text-muted-foreground">Public</div>
            {PUBLIC_NAV.map((n) => (
              <Link
                key={n.to}
                to={n.to}
                className="flex items-center gap-2.5 rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              >
                <n.icon className="h-4 w-4" />
                {n.label}
                <ExternalLink className="ml-auto h-3 w-3 opacity-50" />
              </Link>
            ))}
          </nav>
        </aside>
        <main className="min-w-0 flex-1">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

function Header() {
  const { user, signOut } = useAuth();
  const nav = useNavigate();
  return (
    <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur">
      <div className="mx-auto flex max-w-7xl items-center justify-between gap-4 px-4 py-3 md:px-6">
        <div className="flex items-center gap-6">
          <Link to="/app" className="flex items-center gap-2 font-display text-xl font-semibold tracking-tight">
            <span className="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground">
              <KeyRound className="h-3.5 w-3.5" />
            </span>
            HypiToken
          </Link>
          <nav className="hidden items-center gap-1 md:flex">
            <Link to="/" className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">Home</Link>
            <Link to="/pricing" className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">Pricing</Link>
            <Link to="/docs" className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">Docs</Link>
            <Link to="/status" className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">Status</Link>
          </nav>
        </div>
        <div className="flex items-center gap-2">
          {user && (
            <div className="hidden items-center gap-3 rounded-md border border-border-strong bg-card px-3 py-1.5 sm:flex">
              <span className="text-xs uppercase text-muted-foreground tracking-wider">Balance</span>
              <span className="font-mono text-sm font-semibold tabular-nums">{fmtUSD(user.balance_usd)}</span>
            </div>
          )}
          <ThemeToggle />
          {user ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                signOut();
                nav("/login");
              }}
              className="gap-1.5"
            >
              <LogOut className="h-3.5 w-3.5" />
              <span className="hidden sm:inline">Sign out</span>
            </Button>
          ) : (
            <Button size="sm" onClick={() => nav("/login")}>
              <UserIcon className="h-3.5 w-3.5 mr-1.5" /> Sign in
            </Button>
          )}
        </div>
      </div>
    </header>
  );
}

export function PublicHeader() {
  const { user } = useAuth();
  return (
    <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur">
      <div className="mx-auto flex max-w-7xl items-center justify-between gap-4 px-4 py-3 md:px-6">
        <Link to="/" className="flex items-center gap-2 font-display text-xl font-semibold tracking-tight">
          <span className="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground">
            <KeyRound className="h-3.5 w-3.5" />
          </span>
          HypiToken
        </Link>
        <nav className="hidden items-center gap-1 sm:flex">
          <NavLink to="/" end className={({ isActive }) => cn("rounded-md px-3 py-1.5 text-sm transition-colors", isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}>Home</NavLink>
          <NavLink to="/pricing" className={({ isActive }) => cn("rounded-md px-3 py-1.5 text-sm transition-colors", isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}>Pricing</NavLink>
          <NavLink to="/docs" className={({ isActive }) => cn("rounded-md px-3 py-1.5 text-sm transition-colors", isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}>Docs</NavLink>
          <NavLink to="/status" className={({ isActive }) => cn("rounded-md px-3 py-1.5 text-sm transition-colors", isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}>Status</NavLink>
        </nav>
        <div className="flex items-center gap-2">
          <ThemeToggle />
          {user ? (
            <Button asChild size="sm">
              <Link to="/app">Open dashboard →</Link>
            </Button>
          ) : (
            <>
              <Button asChild variant="ghost" size="sm">
                <Link to="/login">Sign in</Link>
              </Button>
              <Button asChild size="sm">
                <Link to="/register">Get started</Link>
              </Button>
            </>
          )}
        </div>
      </div>
    </header>
  );
}
