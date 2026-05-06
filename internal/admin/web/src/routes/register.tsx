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

export default function RegisterPage() {
  const { user, signIn } = useAuth();
  const nav = useNavigate();
  const { t } = useTranslation();
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
      toast.success(t("auth.register.codeSent"));
      setStep("verify");
    } catch (e: any) {
      toast.error(e.message || t("auth.register.codeSendFailed"));
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
      toast.success(t("auth.register.created"));
      nav("/app");
    } catch (e: any) {
      toast.error(e.message || t("auth.register.registerFailed"));
    } finally {
      setBusy(false);
    }
  };

  if (step === "verify") {
    return (
      <AuthLayout title={t("auth.register.verifyTitle")} sub={t("auth.register.verifySub", { email })}>
        <form onSubmit={register} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="code">{t("auth.register.codeLabel")}</Label>
            <Input id="code" inputMode="numeric" pattern="[0-9]*" maxLength={6} required value={code} onChange={(e) => setCode(e.target.value)} className="font-mono text-lg tracking-widest text-center" />
          </div>
          <Button type="submit" className="w-full" disabled={busy}>{busy ? t("auth.register.verifying") : t("auth.register.verifySubmit")}</Button>
          <Button type="button" variant="ghost" className="w-full" onClick={() => setStep("start")}>{t("common.back")}</Button>
        </form>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout title={t("auth.register.title")} sub={t("auth.register.sub")}>
      <form onSubmit={sendCode} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="email">{t("common.email")}</Label>
          <Input id="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        <div className="space-y-2">
          <Label htmlFor="password">{t("common.password")}</Label>
          <Input id="password" type="password" autoComplete="new-password" minLength={8} required value={password} onChange={(e) => setPassword(e.target.value)} />
          <p className="text-xs text-muted-foreground">{t("auth.register.passwordHint")}</p>
        </div>
        <Button type="submit" className="w-full" disabled={busy}>{busy ? t("auth.register.sending") : t("auth.register.sendCode")}</Button>
        <div className="text-center text-sm text-muted-foreground">
          {t("auth.register.already")} <Link to="/login" className="text-primary underline-offset-4 hover:underline">{t("auth.login.title")}</Link>
        </div>
      </form>
    </AuthLayout>
  );
}
