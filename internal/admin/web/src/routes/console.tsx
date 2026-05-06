import { Dashboard } from "@/legacy/components/dashboard";
import { useAuth } from "@/hooks/use-auth";
import { Navigate } from "react-router-dom";

// /app/console hosts the original CPA-Claude operator console (Overview,
// Credentials, Tokens, Requests, Pricing) ported wholesale into the
// hypitoken SaaS shell. Authentication is SSO'd through the SaaS JWT —
// the legacy panel's API client sends `Authorization: Bearer <jwt>` and
// the /mgmt-console gate accepts that for logged-in users.
//
// Read-only endpoints (everything Overview/Pricing pull) are open to any
// signed-in user. Mutations (credential upload, token CRUD, etc.) are
// gated to role=admin server-side, so non-admins simply see toasts when
// they try to mutate.
export default function ConsolePage() {
  const { user, signOut } = useAuth();
  if (!user) return <Navigate to="/login" replace />;
  return <Dashboard onLogout={signOut} />;
}
