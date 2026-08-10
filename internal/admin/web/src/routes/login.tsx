import { KeyRound, Layers, Receipt, ShieldCheck } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { type FormEvent, lazy, type ReactNode, Suspense, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useLocation, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { HlsVideo } from "@/components/landing/hls-video";
import { useIsMobile, usePrefersReducedMotion } from "@/components/landing/use-media";
import { LanguageToggle } from "@/components/language-toggle";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/hooks/use-auth";
import { apiPost } from "@/lib/api";
import type { User } from "@/lib/types";
import { cn, errMsg, errStatus } from "@/lib/utils";

const ParticleField = lazy(() => import("@/components/landing/particle-field"));

const EASE = [0.22, 1, 0.36, 1] as const;

// Two cinematic clips. The form sits on `side`; the video fills the opposite
// half — login/reset put the form on the right (video left), register flips it.
const VIDEO_LEFT = "https://video.wjsphy.top/auth-left.mp4";
const VIDEO_RIGHT = "https://video.wjsphy.top/auth-right.mp4";

// Tactile press + lift, shared by every auth submit button. Pure CSS so it
// stays snappy and honours reduced motion via the browser.
export const authBtn =
  "transition-transform duration-200 ease-out hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98]";

export default function LoginPage() {
  const { user, signIn } = useAuth();
  const nav = useNavigate();
  const loc = useLocation();
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);

  const locState = loc.state as { from?: string } | null;
  if (user) return <Navigate to={locState?.from ?? "/app"} replace />;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await apiPost<{ token: string; user: User }>("/auth/login", { email, password });
      signIn(r.token, r.user);
      toast.success(t("auth.login.welcomeBack"));
      nav("/app");
    } catch (e) {
      // A disabled account is the one login failure with somewhere to go: the
      // appeal channel. Anti-abuse enforcement is probabilistic and does catch
      // real users, so the 403 has to offer them the door rather than a dead end.
      if (errStatus(e) === 403) {
        toast.error(t("auth.login.disabled"), {
          action: {
            label: t("auth.login.appealAction"),
            onClick: () => nav(`/appeal?email=${encodeURIComponent(email)}`),
          },
          duration: 10000,
        });
      } else {
        toast.error(errMsg(e, t("auth.login.invalidCredentials")));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthLayout side="right" title={t("auth.login.title")} sub={t("auth.login.sub")}>
      <AuthForm onSubmit={submit} className="space-y-4">
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
        <AuthRow className="space-y-2">
          <div className="flex items-center justify-between">
            <Label htmlFor="password">{t("common.password")}</Label>
            <Link
              to="/forgot-password"
              className="text-xs text-muted-foreground transition-colors hover:text-primary hover:underline underline-offset-4"
            >
              {t("auth.login.forgotPassword")}
            </Link>
          </div>
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </AuthRow>
        <AuthRow>
          <Button type="submit" className={cn("w-full", authBtn)} disabled={busy}>
            {busy ? `${t("auth.login.title")}…` : t("auth.login.submit")}
          </Button>
        </AuthRow>
        <AuthRow className="text-center text-sm text-muted-foreground">
          {t("auth.login.noAccount")}{" "}
          <Link to="/register" className="text-primary underline-offset-4 hover:underline">
            {t("auth.login.createOne")}
          </Link>
        </AuthRow>
      </AuthForm>
    </AuthLayout>
  );
}

// ─── Motion form helpers — staggered field entrance, reused by every page ─────

const fieldStagger = {
  hidden: {},
  show: { transition: { staggerChildren: 0.07, delayChildren: 0.12 } },
};
const fieldItem = {
  hidden: { opacity: 0, y: 12, filter: "blur(6px)" },
  show: { opacity: 1, y: 0, filter: "blur(0px)", transition: { duration: 0.45, ease: EASE } },
};

export function AuthForm({
  children,
  onSubmit,
  className,
}: {
  children: ReactNode;
  onSubmit: (e: FormEvent) => void;
  className?: string;
}) {
  const reduce = useReducedMotion();
  if (reduce)
    return (
      <form onSubmit={onSubmit} className={className}>
        {children}
      </form>
    );
  return (
    <motion.form
      onSubmit={onSubmit}
      className={className}
      variants={fieldStagger}
      initial="hidden"
      animate="show"
    >
      {children}
    </motion.form>
  );
}

export function AuthRow({ children, className }: { children: ReactNode; className?: string }) {
  const reduce = useReducedMotion();
  if (reduce) return <div className={className}>{children}</div>;
  return (
    <motion.div variants={fieldItem} className={className}>
      {children}
    </motion.div>
  );
}

// ─── Split-screen auth shell — cinematic video on one half, glass form on the
// other. `side` is the side the FORM occupies (video fills the opposite). ─────

export function AuthLayout({
  title,
  sub,
  children,
  side = "right",
}: {
  title: string;
  sub?: string;
  children: ReactNode;
  side?: "left" | "right";
}) {
  const isMobile = useIsMobile(1024);
  const reduce = usePrefersReducedMotion();
  const showParticles = !isMobile && !reduce;
  const videoOnLeft = side === "right";
  const videoSrc = videoOnLeft ? VIDEO_LEFT : VIDEO_RIGHT;

  const videoPanel = (
    <div className="dark relative hidden overflow-hidden lg:block">
      <HlsVideo
        src={videoSrc}
        className="kenburns"
        style={{ opacity: 0.8 }}
        fallbackColor="#06120e"
      />
      {/* legibility scrims + a primary-tinted wash toward the form seam */}
      <div
        aria-hidden
        className="absolute inset-0"
        style={{
          background:
            "linear-gradient(to top, rgba(4,12,9,0.85) 0%, rgba(4,12,9,0.25) 45%, rgba(4,12,9,0.45) 100%)",
        }}
      />
      <div
        aria-hidden
        className="absolute inset-0"
        style={{
          background: videoOnLeft
            ? "linear-gradient(to right, transparent 55%, color-mix(in oklch, var(--primary) 16%, transparent) 100%)"
            : "linear-gradient(to left, transparent 55%, color-mix(in oklch, var(--primary) 16%, transparent) 100%)",
        }}
      />
      <div className="noise pointer-events-none absolute inset-0 opacity-[0.16]" aria-hidden />

      <div className="relative z-10 flex h-full flex-col justify-between p-10 text-white xl:p-14">
        <Link
          to="/"
          className="inline-flex w-fit items-center gap-2 font-display text-xl font-semibold"
        >
          <span className="grid h-8 w-8 place-items-center rounded-lg bg-primary text-primary-foreground shadow-lg">
            <KeyRound className="h-4 w-4" />
          </span>
          HypiToken
        </Link>

        <motion.div
          initial={reduce ? false : { opacity: 0, y: 24, filter: "blur(10px)" }}
          animate={{ opacity: 1, y: 0, filter: "blur(0px)" }}
          transition={{ duration: 0.8, ease: EASE, delay: 0.1 }}
          className="max-w-md"
        >
          <h2 className="whitespace-pre-line font-display text-3xl font-semibold leading-[1.12] tracking-tight xl:text-4xl">
            <PanelTagline />
          </h2>
          <p className="mt-4 text-sm leading-relaxed text-white/70">
            <PanelSub />
          </p>
          <PanelChips />
        </motion.div>
      </div>
    </div>
  );

  const formPanel = (
    <div className="relative flex items-center justify-center overflow-hidden px-4 py-10 sm:px-8">
      {/* ambient tech backdrop: particle field + focal glow behind the card */}
      {showParticles && (
        <div aria-hidden className="pointer-events-none absolute inset-0 opacity-[0.38]">
          <Suspense fallback={null}>
            <ParticleField count={1500} />
          </Suspense>
        </div>
      )}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0"
        style={{
          background:
            "radial-gradient(ellipse 50% 45% at 50% 38%, color-mix(in oklch, var(--primary) 13%, transparent), transparent 72%)",
        }}
      />

      <div className="absolute right-4 top-4 z-20 flex items-center gap-1">
        <LanguageToggle />
        <ThemeToggle />
      </div>
      {/* mobile-only brand (video panel that normally carries it is hidden) */}
      <Link
        to="/"
        className="absolute left-4 top-4 z-20 flex items-center gap-2 font-display text-lg font-semibold lg:hidden"
      >
        <span className="grid h-7 w-7 place-items-center rounded-md bg-primary text-primary-foreground">
          <KeyRound className="h-3.5 w-3.5" />
        </span>
        HypiToken
      </Link>

      <motion.div
        initial={
          reduce ? false : { opacity: 0, x: side === "right" ? 36 : -36, filter: "blur(8px)" }
        }
        animate={{ opacity: 1, x: 0, filter: "blur(0px)" }}
        transition={{ duration: 0.55, ease: EASE }}
        className="glass relative z-10 w-full max-w-sm rounded-3xl p-7 shadow-2xl sm:p-8"
      >
        <div className="mb-6">
          <h1 className="font-display text-2xl font-semibold tracking-tight">{title}</h1>
          {sub && <p className="mt-1.5 text-sm text-muted-foreground">{sub}</p>}
        </div>
        {children}
      </motion.div>
    </div>
  );

  return (
    <div className="relative min-h-dvh w-full overflow-hidden bg-background text-foreground">
      <div className="grid min-h-dvh lg:grid-cols-2">
        {videoOnLeft ? (
          <>
            {videoPanel}
            {formPanel}
          </>
        ) : (
          <>
            {formPanel}
            {videoPanel}
          </>
        )}
      </div>
    </div>
  );
}

function PanelTagline() {
  const { t } = useTranslation();
  return <>{t("auth.panel.tagline")}</>;
}
function PanelSub() {
  const { t } = useTranslation();
  return <>{t("auth.panel.sub")}</>;
}

function PanelChips() {
  const { t } = useTranslation();
  const reduce = usePrefersReducedMotion();
  const chips = [
    { icon: Layers, label: t("auth.panel.f1") },
    { icon: Receipt, label: t("auth.panel.f2") },
    { icon: ShieldCheck, label: t("auth.panel.f3") },
  ];
  return (
    <motion.ul
      className="mt-7 flex flex-col gap-2.5"
      initial={reduce ? false : "hidden"}
      animate="show"
      variants={{ hidden: {}, show: { transition: { staggerChildren: 0.1, delayChildren: 0.45 } } }}
    >
      {chips.map((c) => (
        <motion.li
          key={c.label}
          variants={{
            hidden: { opacity: 0, x: -16 },
            show: { opacity: 1, x: 0, transition: { duration: 0.5, ease: EASE } },
          }}
          className="glass-dark inline-flex w-fit items-center gap-2.5 rounded-full px-4 py-2 text-sm font-medium"
        >
          <c.icon className="h-4 w-4 text-primary" />
          {c.label}
        </motion.li>
      ))}
    </motion.ul>
  );
}
