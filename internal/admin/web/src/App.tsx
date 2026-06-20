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
import AdminPage from "@/routes/admin";
import BillingPage from "@/routes/billing";
import ConsolePage from "@/routes/console";
import DashboardPage from "@/routes/dashboard";
import DocsLayout, { DocsIndex } from "@/routes/docs";
import ForgotPasswordPage from "@/routes/forgot-password";
import HomePage from "@/routes/home";
import LoginPage from "@/routes/login";
import LogsPage from "@/routes/logs";

// Leaderboard pulls in a canvas pixel-office renderer + SSE — lazy-load it so
// the dashboard bundle isn't burdened for users who never open the arena.
const LeaderboardPage = lazy(() => import("@/routes/leaderboard"));

import PricingPage from "@/routes/pricing";
import PrivacyPage from "@/routes/privacy";
import RegisterPage from "@/routes/register";
import StatusPage from "@/routes/status";
import TermsPage from "@/routes/terms";
import TokensPage from "@/routes/tokens";

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
          <Routes>
            {/* standalone public pages — each owns its own full-screen layout
                (home = cinematic video hero; auth = split-screen) */}
            <Route path="/" element={<HomePage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/register" element={<RegisterPage />} />
            <Route path="/forgot-password" element={<ForgotPasswordPage />} />

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
              <Route
                path="leaderboard"
                element={
                  <Suspense fallback={null}>
                    <LeaderboardPage />
                  </Suspense>
                }
              />
              <Route path="tokens" element={<TokensPage />} />
              <Route path="billing" element={<BillingPage />} />
              <Route path="logs" element={<LogsPage />} />
              <Route path="console" element={<ConsolePage />} />
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
        </BrowserRouter>
      </ConfirmProvider>
      <Toaster position="top-right" richColors closeButton />
    </AuthProvider>
  );
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
