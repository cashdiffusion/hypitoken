import { type FormEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { OtpField } from "@/components/auth/otp-field";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { OtpState } from "@/components/ui/input-otp";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/hooks/use-auth";
import { apiPost } from "@/lib/api";
import { cn, errMsg } from "@/lib/utils";
import { AuthForm, AuthLayout, AuthRow, authBtn } from "./login";

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
  const [otpState, setOtpState] = useState<OtpState>("idle");

  if (user) return <Navigate to="/app" replace />;

  const sendCode = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await apiPost("/auth/send-code", { email, purpose: "reset" });
      toast.success(t("auth.forgot.codeSent"));
      setStep("reset");
    } catch (e) {
      toast.error(errMsg(e, t("auth.register.codeSendFailed")));
    } finally {
      setBusy(false);
    }
  };

  const resetPassword = async (e: FormEvent) => {
    e.preventDefault();
    if (busy || otpState === "success") return;
    if (code.length !== 6) {
      setOtpState("error");
      window.setTimeout(() => setOtpState("idle"), 650);
      return;
    }
    setBusy(true);
    setOtpState("verifying");
    try {
      await apiPost("/auth/reset-password", { email, code, new_password: newPassword });
      setOtpState("success");
      toast.success(t("auth.forgot.reset"));
      // hold on the celebration before sending them to sign in
      window.setTimeout(() => nav("/login"), 1200);
    } catch (e) {
      setOtpState("error");
      setBusy(false);
      toast.error(errMsg(e, t("common.error")));
      window.setTimeout(() => {
        setCode("");
        setOtpState("idle");
      }, 650);
    }
  };

  if (step === "reset") {
    return (
      <AuthLayout
        side="right"
        title={t("auth.forgot.step2Title")}
        sub={t("auth.forgot.step2Sub", { email })}
      >
        <AuthForm key="reset" onSubmit={resetPassword} className="space-y-5">
          <AuthRow>
            <OtpField
              value={code}
              onChange={setCode}
              onComplete={() => document.getElementById("new-password")?.focus()}
              state={otpState}
              disabled={busy}
            />
          </AuthRow>
          <AuthRow className="space-y-2">
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
          </AuthRow>
          <AuthRow>
            <Button
              type="submit"
              className={cn("w-full", authBtn)}
              disabled={busy || code.length !== 6 || otpState === "success"}
            >
              {otpState === "success"
                ? t("auth.forgot.verified")
                : busy
                  ? t("auth.forgot.resetting")
                  : t("auth.forgot.submit")}
            </Button>
          </AuthRow>
          <AuthRow>
            <Button
              type="button"
              variant="ghost"
              className={cn("w-full", authBtn)}
              disabled={busy}
              onClick={() => {
                setOtpState("idle");
                setCode("");
                setStep("start");
              }}
            >
              {t("common.back")}
            </Button>
          </AuthRow>
        </AuthForm>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout side="right" title={t("auth.forgot.title")} sub={t("auth.forgot.sub")}>
      <AuthForm key="start" onSubmit={sendCode} className="space-y-4">
        <AuthRow className="space-y-2">
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
        </AuthRow>
        <AuthRow>
          <Button type="submit" className={cn("w-full", authBtn)} disabled={busy}>
            {busy ? t("auth.forgot.sending") : t("auth.forgot.sendCode")}
          </Button>
        </AuthRow>
        <AuthRow className="text-center text-sm text-muted-foreground">
          <Link to="/login" className="text-primary underline-offset-4 hover:underline">
            {t("auth.forgot.backToLogin")}
          </Link>
        </AuthRow>
      </AuthForm>
    </AuthLayout>
  );
}
