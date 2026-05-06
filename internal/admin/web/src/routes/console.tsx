import { useState } from "react";
import { Dashboard } from "@/legacy/components/dashboard";
import { Login } from "@/legacy/components/login";
import { getToken } from "@/legacy/lib/api";

// /app/console hosts the original CPA-Claude operator console (Overview,
// Credentials, Tokens, Requests, Pricing) ported wholesale into the
// hypitoken SaaS shell. It uses the legacy X-Admin-Token auth — orthogonal
// to the SaaS JWT — so an operator who's already signed in still has to
// supply the legacy admin password once. Token persists in localStorage.
export default function ConsolePage() {
  const [authed, setAuthed] = useState(!!getToken());
  if (!authed) return <Login onOk={() => setAuthed(true)} />;
  return <Dashboard onLogout={() => setAuthed(false)} />;
}
