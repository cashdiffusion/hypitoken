import { lazy, Suspense, useEffect, useRef } from "react";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { Toaster } from "sonner";
import { AppShell, PublicLayout } from "@/components/layout/shell";
import { RequireAdmin, RequireAuth } from "@/components/require-auth";
import { TitleWatcher } from "@/components/title-watcher";
import { ConfirmProvider } from "@/components/ui/confirm-dialog";
import { AuthProvider } from "@/hooks/use-auth";
import { initWebAnalytics, pathToPage, trackPageview } from "@/lib/analytics";
import { initAttribution } from "@/lib/attribution";
import HomePage from "@/routes/home";

// Every route except the landing page is code-split. Before this, all of them
// (docs' markdown+highlight.js, the charts, Stripe, the admin panel) shipped in
// one 2.78 MB chunk that every first-time visitor downloaded and parsed before
// the hero could paint. Keep HomePage eager — it *is* the first paint.
const AdminPage = lazy(() => import("@/routes/admin"));
const AppealPage = lazy(() => import("@/routes/appeal"));
const BillingPage = lazy(() => import("@/routes/billing"));
const ConsolePage = lazy(() => import("@/routes/console"));
const DashboardPage = lazy(() => import("@/routes/dashboard"));
const DocsIndex = lazy(() => import("@/routes/docs").then((m) => ({ default: m.DocsIndex })));
const DocsLayout = lazy(() => import("@/routes/docs"));
const ForgotPasswordPage = lazy(() => import("@/routes/forgot-password"));
const InvitePage = lazy(() => import("@/routes/invite"));
const JoinPage = lazy(() => import("@/routes/join"));
const LeaderboardPage = lazy(() => import("@/routes/leaderboard"));
const LoginPage = lazy(() => import("@/routes/login"));
const LogsPage = lazy(() => import("@/routes/logs"));
const PricingPage = lazy(() => import("@/routes/pricing"));
const PrivacyPage = lazy(() => import("@/routes/privacy"));
const RegisterPage = lazy(() => import("@/routes/register"));
const StatusPage = lazy(() => import("@/routes/status"));
const SupportPage = lazy(() => import("@/routes/support"));
const TermsPage = lazy(() => import("@/routes/terms"));
const TokensPage = lazy(() => import("@/routes/tokens"));
const UsagePage = lazy(() => import("@/routes/usage"));
const WorkspacePage = lazy(() => import("@/routes/workspace"));

export default function App() {
  // Capture marketing attribution (?ref=) and start site-wide visitor-behaviour
  // tracking on first load. Both best-effort and self-contained — see
  // lib/attribution and lib/analytics.
  useEffect(() => {
    initAttribution();
    initWebAnalytics();
  }, []);

  return (
    <AuthProvider>
      <ConfirmProvider>
        <BrowserRouter>
          <TitleWatcher />
          <RouteTracker />
          {/* One boundary for every code-split route. React Router v7 wraps
              navigations in startTransition, so an in-app navigation keeps the
              current page on screen while the next chunk loads — the fallback
              only ever shows on a cold deep-link. */}
          <Suspense fallback={<RouteFallback />}>
            <Routes>
              {/* standalone public pages — each owns its own full-screen layout
                (home = cinematic video hero; auth = split-screen) */}
              <Route path="/" element={<HomePage />} />
              <Route path="/login" element={<LoginPage />} />
              <Route path="/register" element={<RegisterPage />} />
              <Route path="/forgot-password" element={<ForgotPasswordPage />} />
              {/* Appeal is reachable with no session — it is the only channel a
                  disabled account has left, so it must not sit behind RequireAuth. */}
              <Route path="/appeal" element={<AppealPage />} />
              <Route path="/join/:token" element={<JoinPage />} />

              {/* marketing pages — share one persistent PublicLayout header so the
                nav never remounts (no flicker) and stays consistent */}
              <Route element={<PublicLayout />}>
                <Route path="/pricing" element={<PricingPage />} />
                <Route path="/status" element={<StatusPage />} />
                <Route path="/docs" element={<DocsIndex />} />
                <Route path="/docs/:slug" element={<DocsLayout />} />
                <Route path="/terms" element={<TermsPage />} />
                <Route path="/privacy" element={<PrivacyPage />} />
              </Route>

              {/* authed */}
              <Route
                path="/app"
                element={
                  <RequireAuth>
                    <AppShell />
                  </RequireAuth>
                }
              >
                <Route index element={<DashboardPage />} />
                <Route path="leaderboard" element={<LeaderboardPage />} />
                <Route path="invite" element={<InvitePage />} />
                <Route path="tokens" element={<TokensPage />} />
                <Route path="workspace" element={<WorkspacePage />} />
                <Route path="billing" element={<BillingPage />} />
                <Route path="logs" element={<LogsPage />} />
                <Route path="usage" element={<UsagePage />} />
                <Route path="console" element={<ConsolePage />} />
                <Route path="support" element={<SupportPage />} />
                <Route
                  path="admin/*"
                  element={
                    <RequireAdmin>
                      <AdminPage />
                    </RequireAdmin>
                  }
                />
              </Route>

              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Suspense>
        </BrowserRouter>
      </ConfirmProvider>
      <Toaster position="top-right" richColors closeButton />
    </AuthProvider>
  );
}

// RouteFallback covers the gap while a route's chunk downloads on a cold
// deep-link. Deliberately near-invisible: a spinner that flashes for 80 ms
// reads as jank, whereas a still background reads as "still loading".
function RouteFallback() {
  return <div className="min-h-screen bg-background" aria-busy="true" />;
}

// RouteTracker reports a pageview on every SPA route change. The landing
// pageview is already sent by initWebAnalytics, so the first render is skipped
// to avoid double-counting it. Best-effort — see lib/analytics.
function RouteTracker() {
  const { pathname } = useLocation();
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    trackPageview(pathToPage(pathname));
  }, [pathname]);
  return null;
}
