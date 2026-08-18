import { type FormEvent, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { OtpField } from "@/components/auth/otp-field";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import type { OtpState } from "@/components/ui/input-otp";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/hooks/use-auth";
import { apiPost } from "@/lib/api";
import type { User } from "@/lib/types";
import { cn, errMsg } from "@/lib/utils";
import { AuthForm, AuthLayout, AuthRow, authBtn } from "./login";

export default function RegisterPage() {
  const { user, signIn } = useAuth();
  const nav = useNavigate();
  const { t } = useTranslation();
  const [step, setStep] = useState<"start" | "verify">("start");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [otpState, setOtpState] = useState<OtpState>("idle");
  const [agreed, setAgreed] = useState(false);

  if (user) return <Navigate to="/app" replace />;

  const sendCode = async (e: FormEvent) => {
    e.preventDefault();
    if (!agreed) {
      toast.error(t("auth.register.mustAgree"));
      return;
    }
    setBusy(true);
    try {
      await apiPost("/auth/send-code", { email, purpose: "verify" });
      toast.success(t("auth.register.codeSent"));
      setStep("verify");
    } catch (e) {
      toast.error(errMsg(e, t("auth.register.codeSendFailed")));
    } finally {
      setBusy(false);
    }
  };

  // codeArg lets onComplete submit the just-typed value without waiting for the
  // code state to flush; the form-submit path passes nothing and uses state.
  const register = async (e?: FormEvent, codeArg?: string) => {
    e?.preventDefault();
    const c = codeArg ?? code;
    if (c.length !== 6 || busy || otpState === "success") return;
    setBusy(true);
    setOtpState("verifying");
    try {
      const r = await apiPost<{ token: string; user: User }>("/auth/register", {
        email,
        password,
        code: c,
      });
      setOtpState("success");
      toast.success(t("auth.register.created"));
      // hold on the celebration (confetti + green cells) before navigating.
      window.setTimeout(() => {
        signIn(r.token, r.user);
        nav("/app");
      }, 1200);
    } catch (e) {
      setOtpState("error");
      setBusy(false);
      toast.error(errMsg(e, t("auth.register.registerFailed")));
      // clear the cells and let the user retry after the shake settles
      window.setTimeout(() => {
        setCode("");
        setOtpState("idle");
      }, 650);
    }
  };

  if (step === "verify") {
    return (
      <AuthLayout
        side="left"
        title={t("auth.register.verifyTitle")}
        sub={t("auth.register.verifySub", { email })}
      >
        <AuthForm key="verify" onSubmit={register} className="space-y-6">
          <AuthRow>
            <OtpField
              value={code}
              onChange={setCode}
              onComplete={(c) => register(undefined, c)}
              state={otpState}
              disabled={busy}
            />
          </AuthRow>
          <AuthRow>
            <Button
              type="submit"
              className={cn("w-full", authBtn)}
              disabled={busy || code.length !== 6 || otpState === "success"}
            >
              {otpState === "success"
                ? t("auth.register.verified")
                : busy
                  ? t("auth.register.verifying")
                  : t("auth.register.verifySubmit")}
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
    <AuthLayout side="left" title={t("auth.register.title")} sub={t("auth.register.sub")}>
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
          />
        </AuthRow>
        <AuthRow className="space-y-2">
          <Label htmlFor="password">{t("common.password")}</Label>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            minLength={8}
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <p className="text-xs text-muted-foreground">{t("auth.register.passwordHint")}</p>
        </AuthRow>
        <AuthRow>
          <label
            htmlFor="agree-tos"
            className="flex cursor-pointer items-start gap-2.5 text-xs leading-relaxed text-muted-foreground"
          >
            <Checkbox
              id="agree-tos"
              checked={agreed}
              onCheckedChange={(v) => setAgreed(v === true)}
              className="mt-0.5"
              aria-label={t("auth.register.agreeAria")}
            />
            <span>
              <Trans i18nKey="auth.register.agree">
                I have read and agree to the
                <Link
                  to="/terms"
                  className="text-primary underline-offset-4 hover:underline"
                  target="_blank"
                >
                  Terms of Service
                </Link>
                and
                <Link
                  to="/privacy"
                  className="text-primary underline-offset-4 hover:underline"
                  target="_blank"
                >
                  Privacy Policy
                </Link>
                .
              </Trans>
            </span>
          </label>
        </AuthRow>
        <AuthRow>
          <Button type="submit" className={cn("w-full", authBtn)} disabled={busy || !agreed}>
            {busy ? t("auth.register.sending") : t("auth.register.sendCode")}
          </Button>
        </AuthRow>
        <AuthRow className="text-center text-sm text-muted-foreground">
          {t("auth.register.already")}{" "}
          <Link to="/login" className="text-primary underline-offset-4 hover:underline">
            {t("auth.login.title")}
          </Link>
        </AuthRow>
      </AuthForm>
    </AuthLayout>
  );
}
