import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation();
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
      toast.success(t("auth.forgot.codeSent"));
      setStep("reset");
    } catch (e: any) {
      toast.error(e.message || t("auth.register.codeSendFailed"));
    } finally {
      setBusy(false);
    }
  };

  const resetPassword = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await apiPost("/auth/reset-password", { email, code, new_password: newPassword });
      toast.success(t("auth.forgot.reset"));
      nav("/login");
    } catch (e: any) {
      toast.error(e.message || t("common.error"));
    } finally {
      setBusy(false);
    }
  };

  if (step === "reset") {
    return (
      <AuthLayout title={t("auth.forgot.step2Title")} sub={t("auth.forgot.step2Sub", { email })}>
        <form onSubmit={resetPassword} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="code">{t("auth.register.codeLabel")}</Label>
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
            <Label htmlFor="new-password">{t("auth.forgot.newPasswordLabel")}</Label>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              minLength={8}
              required
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">{t("auth.forgot.newPasswordHint")}</p>
          </div>
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? t("auth.forgot.resetting") : t("auth.forgot.submit")}
          </Button>
          <Button
            type="button"
            variant="ghost"
            className="w-full"
            disabled={busy}
            onClick={() => setStep("start")}
          >
            {t("common.back")}
          </Button>
        </form>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout title={t("auth.forgot.title")} sub={t("auth.forgot.sub")}>
      <form onSubmit={sendCode} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="email">{t("common.email")}</Label>
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
          {busy ? t("auth.forgot.sending") : t("auth.forgot.sendCode")}
        </Button>
        <div className="text-center text-sm text-muted-foreground">
          <Link to="/login" className="text-primary underline-offset-4 hover:underline">
            {t("auth.forgot.backToLogin")}
          </Link>
        </div>
      </form>
    </AuthLayout>
  );
}
