import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { apiPost } from "@/lib/api";
import { useAuth } from "@/hooks/use-auth";
import { AuthLayout } from "./login";

export default function RegisterPage() {
  const { user, signIn } = useAuth();
  const nav = useNavigate();
  const [step, setStep] = useState<"start" | "verify">("start");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);

  if (user) return <Navigate to="/app" replace />;

  const sendCode = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await apiPost("/auth/send-code", { email, purpose: "verify" });
      toast.success("Verification code sent. (Check server logs in dev mode.)");
      setStep("verify");
    } catch (e: any) {
      toast.error(e.message || "Could not send code");
    } finally {
      setBusy(false);
    }
  };

  const register = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await apiPost<any>("/auth/register", { email, password, code });
      signIn(r.token, r.user);
      toast.success("Account created.");
      nav("/app");
    } catch (e: any) {
      toast.error(e.message || "Registration failed");
    } finally {
      setBusy(false);
    }
  };

  const skip = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await apiPost<any>("/auth/register", { email, password });
      signIn(r.token, r.user);
      toast.success("Account created (email verification skipped).");
      nav("/app");
    } catch (e: any) {
      toast.error(e.message || "Registration failed");
    } finally {
      setBusy(false);
    }
  };

  if (step === "verify") {
    return (
      <AuthLayout title="Verify email" sub={`We sent a 6-digit code to ${email}.`}>
        <form onSubmit={register} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="code">Code</Label>
            <Input id="code" inputMode="numeric" pattern="[0-9]*" maxLength={6} required value={code} onChange={(e) => setCode(e.target.value)} className="font-mono text-lg tracking-widest text-center" />
          </div>
          <Button type="submit" className="w-full" disabled={busy}>{busy ? "Verifying…" : "Verify & create account"}</Button>
          <Button type="button" variant="ghost" className="w-full" onClick={skip}>Skip verification</Button>
        </form>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout title="Create account" sub="Free to start. $1 minimum top-up.">
      <form onSubmit={sendCode} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        <div className="space-y-2">
          <Label htmlFor="password">Password</Label>
          <Input id="password" type="password" autoComplete="new-password" minLength={8} required value={password} onChange={(e) => setPassword(e.target.value)} />
          <p className="text-xs text-muted-foreground">Min 8 characters.</p>
        </div>
        <Button type="submit" className="w-full" disabled={busy}>{busy ? "Sending code…" : "Send verification code"}</Button>
        <Button type="button" variant="ghost" className="w-full" disabled={busy} onClick={skip}>Continue without verification</Button>
        <div className="text-center text-sm text-muted-foreground">
          Already have an account? <Link to="/login" className="text-primary underline-offset-4 hover:underline">Sign in</Link>
        </div>
      </form>
    </AuthLayout>
  );
}
