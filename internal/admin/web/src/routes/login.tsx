import { useState, type FormEvent } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { apiPost } from "@/lib/api";
import { useAuth } from "@/hooks/use-auth";
import { ThemeToggle } from "@/components/theme-toggle";
import { LanguageToggle } from "@/components/language-toggle";
import { KeyRound } from "lucide-react";

export default function LoginPage() {
  const { user, signIn } = useAuth();
  const nav = useNavigate();
  const loc = useLocation() as any;
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);

  if (user) return <Navigate to={loc.state?.from ?? "/app"} replace />;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await apiPost<any>("/auth/login", { email, password });
      signIn(r.token, r.user);
      toast.success(t("auth.login.welcomeBack"));
      nav("/app");
    } catch (e: any) {
      toast.error(e.message || t("auth.login.invalidCredentials"));
    } finally {
      setBusy(false);
    }
  };

  return <AuthLayout title={t("auth.login.title")} sub={t("auth.login.sub")}>
    <form onSubmit={submit} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="email">{t("common.email")}</Label>
        <Input id="email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" />
      </div>
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <Label htmlFor="password">{t("common.password")}</Label>
          <Link to="/forgot-password" className="text-xs text-muted-foreground hover:text-primary hover:underline underline-offset-4">
            {t("auth.login.forgotPassword")}
          </Link>
        </div>
        <Input id="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
      </div>
      <Button type="submit" className="w-full" disabled={busy}>{busy ? t("auth.login.title") + "…" : t("auth.login.submit")}</Button>
      <div className="text-center text-sm text-muted-foreground">
        {t("auth.login.noAccount")} <Link to="/register" className="text-primary underline-offset-4 hover:underline">{t("auth.login.createOne")}</Link>
      </div>
    </form>
  </AuthLayout>;
}

export function AuthLayout({ title, sub, children }: { title: string; sub?: string; children: React.ReactNode }) {
  return (
    <div className="grid min-h-dvh place-items-center bg-background p-4">
      <div className="absolute right-4 top-4 flex items-center gap-1">
        <LanguageToggle />
        <ThemeToggle />
      </div>
      <Link to="/" className="absolute left-4 top-4 flex items-center gap-2 font-display text-xl font-semibold">
        <span className="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground">
          <KeyRound className="h-3.5 w-3.5" />
        </span>
        HypiToken
      </Link>
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="font-display text-2xl tracking-tight">{title}</CardTitle>
          {sub && <CardDescription>{sub}</CardDescription>}
        </CardHeader>
        <CardContent>{children}</CardContent>
      </Card>
    </div>
  );
}
