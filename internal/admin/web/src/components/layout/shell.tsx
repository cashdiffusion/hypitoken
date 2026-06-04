import { Link, NavLink, Outlet, useNavigate, useLocation } from "react-router-dom";
import { motion } from "motion/react";
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

// SideNavLink — a sidebar row that, when active, renders a shared glass pill
// + a left primary accent bar. The pill uses a motion layoutId so it slides
// between rows as you navigate, the way a polished product sidebar does.
function SideNavLink({ to, end, icon: Icon, label }: { to: string; end?: boolean; icon: typeof Wallet; label: string }) {
  const { pathname } = useLocation();
  const active = end ? pathname === to : pathname === to || pathname.startsWith(to + "/");
  return (
    <NavLink
      to={to}
      end={end}
      className={cn(
        "relative flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors",
        active ? "font-medium text-primary" : "text-muted-foreground hover:text-foreground"
      )}
    >
      {active && (
        <motion.span
          layoutId="app-nav-pill"
          className="glass absolute inset-0 -z-10 rounded-lg"
          transition={{ type: "spring", stiffness: 380, damping: 32 }}
        >
          <span className="absolute left-0 top-1/2 h-5 w-[3px] -translate-y-1/2 rounded-full bg-primary" />
        </motion.span>
      )}
      <Icon className="h-4 w-4" />
      {label}
    </NavLink>
  );
}

export function AppShell() {
  const { user } = useAuth();
  const { t } = useTranslation();
  return (
    <div className="relative min-h-dvh bg-background text-foreground">
      {/* faint ambient gradient mesh — frames the whole app without competing
          with content. Fixed so it stays put while the page scrolls. */}
      <div
        aria-hidden
        className="pointer-events-none fixed inset-0 -z-10"
        style={{
          background:
            "radial-gradient(50% 40% at 12% 0%, color-mix(in oklch, var(--primary) 9%, transparent), transparent 70%)," +
            "radial-gradient(45% 45% at 100% 8%, color-mix(in oklch, var(--info, var(--primary)) 7%, transparent), transparent 72%)",
        }}
      />
      <Header />
      <div className="mx-auto flex max-w-7xl gap-6 px-4 py-6 md:px-6 lg:py-10">
        <aside className="hidden w-56 flex-shrink-0 lg:block">
          <nav className="sticky top-24 flex flex-col gap-1">
            {NAV_ITEMS.map((n) => (
              <SideNavLink key={n.to} to={n.to} end={n.end} icon={n.icon} label={t(n.labelKey)} />
            ))}
            {user?.role === "admin" && (
              <>
                <div className="mt-6 mb-1 px-3 text-xs uppercase tracking-wider text-muted-foreground">{t("nav.operator")}</div>
                <SideNavLink to="/app/admin" icon={Shield} label={t("nav.admin")} />
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
    <header className="sticky top-0 z-40 border-b border-border/60 bg-background/70 backdrop-blur-xl">
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
            <Link to="/app/billing" className="glass hidden items-center gap-2.5 rounded-full px-3.5 py-1.5 transition-shadow hover:shadow-md sm:flex">
              <Wallet className="h-3.5 w-3.5 text-primary" />
              <span className="text-xs uppercase text-muted-foreground tracking-wider">{t("common.balance")}</span>
              <span className="font-mono text-sm font-semibold tabular-nums">{fmtUSD(user.balance_usd)}</span>
            </Link>
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

// PublicHeader — floating glass pill shared by all public pages (pricing,
// docs, status, auth). Sticky (so it occupies flow and never overlaps page
// content) and themed via `.glass`, matching the homepage nav language.
export function PublicHeader() {
  const { user } = useAuth();
  const { t } = useTranslation();
  const linkCls = ({ isActive }: { isActive: boolean }) =>
    cn(
      "rounded-full px-3 py-1.5 text-sm transition-colors",
      isActive ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground"
    );
  return (
    <div className="sticky top-0 z-40 px-4 pt-3 md:px-6">
      <header className="glass mx-auto flex max-w-7xl items-center justify-between gap-4 rounded-full px-3 py-2 md:px-4">
        <Link to="/" className="flex items-center gap-2 pl-1 font-display text-lg font-semibold tracking-tight">
          <span className="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground">
            <KeyRound className="h-3.5 w-3.5" />
          </span>
          HypiToken
        </Link>
        <nav className="hidden items-center gap-1 lg:flex">
          <NavLink to="/" end className={linkCls}>{t("nav.home")}</NavLink>
          <NavLink to="/pricing" className={linkCls}>{t("nav.pricing")}</NavLink>
          <NavLink to="/docs" className={linkCls}>{t("nav.docs")}</NavLink>
          <NavLink to="/status" className={linkCls}>{t("nav.status")}</NavLink>
        </nav>
        <div className="flex items-center gap-2">
          <div className="hidden items-center gap-1.5 lg:flex">
            <LanguageToggle />
            <ThemeToggle />
            {user ? (
              <Button asChild size="sm" className="rounded-full">
                <Link to="/app">{t("nav.dashboard")} →</Link>
              </Button>
            ) : (
              <>
                <Button asChild variant="ghost" size="sm" className="rounded-full">
                  <Link to="/login">{t("nav.signIn")}</Link>
                </Button>
                <Button asChild size="sm" className="rounded-full">
                  <Link to="/register">{t("nav.signUp")}</Link>
                </Button>
              </>
            )}
          </div>
          <MobileMenu variant="public" />
        </div>
      </header>
    </div>
  );
}
