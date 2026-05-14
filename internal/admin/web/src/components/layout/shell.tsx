import { Link, NavLink, Outlet, useNavigate } from "react-router-dom";
import { useAuth } from "@/hooks/use-auth";
import { ThemeToggle } from "@/components/theme-toggle";
import { LanguageToggle } from "@/components/language-toggle";
import { Button } from "@/components/ui/button";
import { LogOut, User as UserIcon, Wallet, KeyRound, LayoutDashboard, Shield, Terminal, Receipt } from "lucide-react";
import { fmtUSD } from "@/lib/utils";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { MobileMenu } from "@/components/layout/mobile-menu";

// NAV_ITEMS is keyed by i18n string id rather than the literal label —
// resolved at render time so language switching reflows the sidebar.
const NAV_ITEMS = [
  { to: "/app", labelKey: "nav.dashboard", icon: LayoutDashboard, end: true },
  { to: "/app/tokens", labelKey: "nav.tokens", icon: KeyRound },
  { to: "/app/billing", labelKey: "nav.billing", icon: Wallet },
  { to: "/app/logs", labelKey: "nav.logs", icon: Receipt },
  { to: "/app/console", labelKey: "nav.console", icon: Terminal },
];

export function AppShell() {
  const { user } = useAuth();
  const { t } = useTranslation();
  return (
    <div className="min-h-dvh bg-background text-foreground">
      <Header />
      <div className="mx-auto flex max-w-7xl gap-6 px-4 py-6 md:px-6 lg:py-10">
        <aside className="hidden w-56 flex-shrink-0 lg:block">
          <nav className="sticky top-24 flex flex-col gap-1">
            {NAV_ITEMS.map((n) => (
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
                {t(n.labelKey)}
              </NavLink>
            ))}
            {user?.role === "admin" && (
              <>
                <div className="mt-6 mb-1 px-3 text-xs uppercase tracking-wider text-muted-foreground">{t("nav.operator")}</div>
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
                  <Shield className="h-4 w-4" /> {t("nav.admin")}
                </NavLink>
              </>
            )}
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
  const { t } = useTranslation();
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
            <Link to="/" className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">{t("nav.home")}</Link>
            <Link to="/pricing" className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">{t("nav.pricing")}</Link>
            <Link to="/docs" className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">{t("nav.docs")}</Link>
            <Link to="/status" className="rounded-md px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground">{t("nav.status")}</Link>
          </nav>
        </div>
        <div className="flex items-center gap-2">
          {user && (
            <div className="hidden items-center gap-3 rounded-md border border-border-strong bg-card px-3 py-1.5 sm:flex">
              <span className="text-xs uppercase text-muted-foreground tracking-wider">{t("common.balance")}</span>
              <span className="font-mono text-sm font-semibold tabular-nums">{fmtUSD(user.balance_usd)}</span>
            </div>
          )}
          <div className="hidden items-center gap-2 lg:flex">
            <LanguageToggle />
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
                <span className="hidden sm:inline">{t("nav.signOut")}</span>
              </Button>
            ) : (
              <Button size="sm" onClick={() => nav("/login")}>
                <UserIcon className="h-3.5 w-3.5 mr-1.5" /> {t("nav.signIn")}
              </Button>
            )}
          </div>
          <MobileMenu variant="app" />
        </div>
      </div>
    </header>
  );
}

export function PublicHeader() {
  const { user } = useAuth();
  const { t } = useTranslation();
  return (
    <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur">
      <div className="mx-auto flex max-w-7xl items-center justify-between gap-4 px-4 py-3 md:px-6">
        <Link to="/" className="flex items-center gap-2 font-display text-xl font-semibold tracking-tight">
          <span className="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground">
            <KeyRound className="h-3.5 w-3.5" />
          </span>
          HypiToken
        </Link>
        <nav className="hidden items-center gap-1 lg:flex">
          <NavLink to="/" end className={({ isActive }) => cn("rounded-md px-3 py-1.5 text-sm transition-colors", isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}>{t("nav.home")}</NavLink>
          <NavLink to="/pricing" className={({ isActive }) => cn("rounded-md px-3 py-1.5 text-sm transition-colors", isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}>{t("nav.pricing")}</NavLink>
          <NavLink to="/docs" className={({ isActive }) => cn("rounded-md px-3 py-1.5 text-sm transition-colors", isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}>{t("nav.docs")}</NavLink>
          <NavLink to="/status" className={({ isActive }) => cn("rounded-md px-3 py-1.5 text-sm transition-colors", isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}>{t("nav.status")}</NavLink>
        </nav>
        <div className="flex items-center gap-2">
          <div className="hidden items-center gap-2 lg:flex">
            <LanguageToggle />
            <ThemeToggle />
            {user ? (
              <Button asChild size="sm">
                <Link to="/app">{t("nav.dashboard")} →</Link>
              </Button>
            ) : (
              <>
                <Button asChild variant="ghost" size="sm">
                  <Link to="/login">{t("nav.signIn")}</Link>
                </Button>
                <Button asChild size="sm">
                  <Link to="/register">{t("nav.signUp")}</Link>
                </Button>
              </>
            )}
          </div>
          <MobileMenu variant="public" />
        </div>
      </div>
    </header>
  );
}
