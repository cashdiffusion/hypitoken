import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Toaster } from "sonner";
import { AuthProvider } from "@/hooks/use-auth";
import { RequireAdmin, RequireAuth } from "@/components/require-auth";
import { AppShell } from "@/components/layout/shell";
import HomePage from "@/routes/home";
import LoginPage from "@/routes/login";
import RegisterPage from "@/routes/register";
import ForgotPasswordPage from "@/routes/forgot-password";
import DashboardPage from "@/routes/dashboard";
import TokensPage from "@/routes/tokens";
import BillingPage from "@/routes/billing";
import PricingPage from "@/routes/pricing";
import StatusPage from "@/routes/status";
import AdminPage from "@/routes/admin";
import DocsLayout, { DocsIndex } from "@/routes/docs";

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          {/* public */}
          <Route path="/" element={<HomePage />} />
          <Route path="/login" element={<LoginPage />} />
          <Route path="/register" element={<RegisterPage />} />
          <Route path="/forgot-password" element={<ForgotPasswordPage />} />
          <Route path="/pricing" element={<PricingPage />} />
          <Route path="/status" element={<StatusPage />} />
          <Route path="/docs" element={<DocsIndex />} />
          <Route path="/docs/:slug" element={<DocsLayout />} />

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
      <Toaster position="top-right" richColors closeButton />
    </AuthProvider>
  );
}
