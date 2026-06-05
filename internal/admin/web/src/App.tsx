import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Toaster } from "sonner";
import { AppShell, PublicLayout } from "@/components/layout/shell";
import { RequireAdmin, RequireAuth } from "@/components/require-auth";
import { TitleWatcher } from "@/components/title-watcher";
import { ConfirmProvider } from "@/components/ui/confirm-dialog";
import { AuthProvider } from "@/hooks/use-auth";
import AdminPage from "@/routes/admin";
import BillingPage from "@/routes/billing";
import ConsolePage from "@/routes/console";
import DashboardPage from "@/routes/dashboard";
import DocsLayout, { DocsIndex } from "@/routes/docs";
import ForgotPasswordPage from "@/routes/forgot-password";
import HomePage from "@/routes/home";
import LoginPage from "@/routes/login";
import LogsPage from "@/routes/logs";
import PricingPage from "@/routes/pricing";
import RegisterPage from "@/routes/register";
import StatusPage from "@/routes/status";
import TokensPage from "@/routes/tokens";

export default function App() {
  return (
    <AuthProvider>
      <ConfirmProvider>
        <BrowserRouter>
          <TitleWatcher />
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
