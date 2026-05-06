import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { apiPost } from "@/lib/api";
import { useAuth } from "@/hooks/use-auth";
import { AuthLayout } from "./login";

// Two-step reset flow that mirrors the backend's /auth/send-code (purpose=reset)
// + /auth/reset-password endpoints. We don't reveal whether the email is
// registered — backend returns {sent:true} either way to dodge enumeration.
export default function ForgotPasswordPage() {
  const { user } = useAuth();
  const nav = useNavigate();
  const [step, setStep] = useState<"start" | "reset">("start");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [busy, setBusy] = useState(false);

  if (user) return <Navigate to="/app" replace />;

  const sendCode = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await apiPost("/auth/send-code", { email, purpose: "reset" });
      toast.success("If that email exists, a code is on its way.");
      setStep("reset");
    } catch (e: any) {
      toast.error(e.message || "Could not send code");
    } finally {
      setBusy(false);
    }
  };

  const resetPassword = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await apiPost("/auth/reset-password", { email, code, new_password: newPassword });
      toast.success("Password updated. Sign in with your new password.");
      nav("/login");
    } catch (e: any) {
      toast.error(e.message || "Reset failed");
    } finally {
      setBusy(false);
    }
  };

  if (step === "reset") {
    return (
      <AuthLayout title="Set a new password" sub={`Enter the 6-digit code we sent to ${email}.`}>
        <form onSubmit={resetPassword} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="code">Code</Label>
            <Input
              id="code"
              inputMode="numeric"
              pattern="[0-9]*"
              maxLength={6}
              required
              value={code}
              onChange={(e) => setCode(e.target.value)}
              className="font-mono text-lg tracking-widest text-center"
              placeholder="000000"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="new-password">New password</Label>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              minLength={8}
              required
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">Min 8 characters.</p>
          </div>
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Resetting…" : "Reset password"}
          </Button>
          <Button
            type="button"
            variant="ghost"
            className="w-full"
            disabled={busy}
            onClick={() => setStep("start")}
          >
            Use a different email
          </Button>
        </form>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout
      title="Forgot password?"
      sub="Enter your account email and we'll send you a code to reset it."
    >
      <form onSubmit={sendCode} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="email">Email</Label>
          <Input
            id="email"
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
          />
        </div>
        <Button type="submit" className="w-full" disabled={busy}>
          {busy ? "Sending code…" : "Send reset code"}
        </Button>
        <div className="text-center text-sm text-muted-foreground">
          Remembered it?{" "}
          <Link to="/login" className="text-primary underline-offset-4 hover:underline">
            Sign in
          </Link>
        </div>
      </form>
    </AuthLayout>
  );
}
