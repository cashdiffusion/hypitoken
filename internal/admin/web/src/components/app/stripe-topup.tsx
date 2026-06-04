import { useEffect, useMemo, useRef, useState } from "react";
import { loadStripe, type Stripe, type Appearance } from "@stripe/stripe-js";
import { Elements, PaymentElement, useStripe, useElements } from "@stripe/react-stripe-js";
import { useTranslation } from "react-i18next";
import { CreditCard, Loader2, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";

// loadStripe is expensive (injects the Stripe.js script) and must be called
// once per publishable key — cache the promise across mounts/keys.
const stripeCache: Record<string, Promise<Stripe | null>> = {};
function getStripe(pk: string): Promise<Stripe | null> {
  if (!stripeCache[pk]) stripeCache[pk] = loadStripe(pk);
  return stripeCache[pk];
}

// cssVarToHex resolves a CSS custom property to a Stripe-safe color. Our theme
// is authored in oklch(); the Stripe appearance API silently REJECTS oklch (and
// other CSS Color-4 functions) and falls back to its default light theme — the
// cause of the white Payment Element in dark mode. We round-trip the computed
// value through a <canvas> fillStyle, which normalizes any CSS color (incl.
// oklch) to a hex/rgb string Stripe accepts.
function cssVarToHex(name: string, fallback: string): string {
  if (typeof document === "undefined") return fallback;
  try {
    const probe = document.createElement("span");
    probe.style.color = `var(${name})`;
    probe.style.display = "none";
    document.body.appendChild(probe);
    const computed = getComputedStyle(probe).color; // may be rgb() or oklch()/color()
    probe.remove();
    const ctx = document.createElement("canvas").getContext("2d");
    if (!ctx) return computed || fallback;
    ctx.fillStyle = "#000000";
    ctx.fillStyle = computed; // invalid colors leave fillStyle unchanged
    return ctx.fillStyle || fallback;
  } catch {
    return fallback;
  }
}

function isDark(): boolean {
  return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
}

// buildAppearance themes the Payment Element to match the app. We use Stripe's
// built-in base theme ("night" in dark mode, "stripe" in light) so backgrounds,
// text and inputs are correctly dark/light without us fighting every variable,
// then override just the brand accent (converted to hex so it isn't rejected).
function buildAppearance(): Appearance {
  const dark = isDark();
  const primary = cssVarToHex("--primary", dark ? "#7be0a3" : "#2f6f5f");
  const danger = cssVarToHex("--destructive", "#ef4444");
  return {
    theme: dark ? "night" : "stripe",
    variables: {
      colorPrimary: primary,
      colorDanger: danger,
      borderRadius: "8px",
      fontSizeBase: "15px",
      spacingUnit: "4px",
      fontFamily: 'ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif',
    },
    rules: {
      ".Input:focus": { border: `1px solid ${primary}`, boxShadow: `0 0 0 1px ${primary}` },
      ".Tab:hover": { border: `1px solid ${primary}` },
      ".Tab--selected": { border: `1px solid ${primary}`, boxShadow: `0 0 0 1px ${primary}` },
    },
  };
}

interface StripeTopUpProps {
  publishableKey: string;
  clientSecret: string;
  returnUrl: string;
  outTradeNo: string;
  amountUsd: number;
  /** Button label for the charged amount — "¥72.50" for a CNY deploy, "$10.00"
   *  for USD. Distinct from amountUsd, which is always the wallet credit. */
  payLabel: string;
  /** called once the PaymentElement reports the intent is settling/settled —
   *  the parent then polls the backend order until it flips to paid. */
  onConfirmed: () => void;
}

// StripeTopUp mounts the embedded Payment Element. Stripe owns the sensitive
// card / Alipay / WeChat / crypto input inside its iframe; we own everything
// around it.
export function StripeTopUp(props: StripeTopUpProps) {
  const { publishableKey, clientSecret } = props;
  const stripePromise = useMemo(() => getStripe(publishableKey), [publishableKey]);
  // Track the light/dark class on <html> so the Payment Element re-themes live
  // when the user flips the theme toggle while the dialog is open.
  const [dark, setDark] = useState(isDark());
  useEffect(() => {
    const el = document.documentElement;
    const obs = new MutationObserver(() => setDark(el.classList.contains("dark")));
    obs.observe(el, { attributes: true, attributeFilter: ["class"] });
    return () => obs.disconnect();
  }, []);
  const appearance = useMemo(() => buildAppearance(), [dark]);
  return (
    <Elements stripe={stripePromise} options={{ clientSecret, appearance, loader: "auto" }}>
      <CheckoutForm {...props} />
    </Elements>
  );
}

function CheckoutForm({ returnUrl, outTradeNo, payLabel, onConfirmed }: StripeTopUpProps) {
  const { t } = useTranslation();
  const stripe = useStripe();
  const elements = useElements();
  const [busy, setBusy] = useState(false);
  const [ready, setReady] = useState(false);
  const [err, setErr] = useState("");
  const confirmedRef = useRef(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!stripe || !elements) return;
    setBusy(true);
    setErr("");
    // return_url carries our order id so the post-redirect billing page knows
    // which order to resume polling for (Alipay/WeChat/crypto bounce away).
    const sep = returnUrl.includes("?") ? "&" : "?";
    const { error, paymentIntent } = await stripe.confirmPayment({
      elements,
      confirmParams: {
        return_url: returnUrl ? `${returnUrl}${sep}out=${encodeURIComponent(outTradeNo)}` : window.location.href,
      },
      // Stay in-page for methods that don't need a redirect (cards); only
      // bounce out when the method requires it (Alipay/WeChat/crypto).
      redirect: "if_required",
    });
    if (error) {
      // validation / card errors land here without leaving the page.
      setErr(error.message || t("billing.stripe.failed"));
      setBusy(false);
      return;
    }
    if (paymentIntent && (paymentIntent.status === "succeeded" || paymentIntent.status === "processing")) {
      if (!confirmedRef.current) {
        confirmedRef.current = true;
        onConfirmed();
      }
    }
    setBusy(false);
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <PaymentElement
        options={{ layout: { type: "tabs", defaultCollapsed: false } }}
        onReady={() => setReady(true)}
      />
      {err && <p className="text-sm text-destructive">{err}</p>}
      <Button type="submit" size="lg" className="w-full gap-2" disabled={!stripe || !ready || busy}>
        {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <CreditCard className="h-4 w-4" />}
        {busy ? t("billing.stripe.processing") : t("billing.stripe.pay", { amount: payLabel })}
      </Button>
      <p className="flex items-center justify-center gap-1.5 text-[11px] text-muted-foreground">
        <ShieldCheck className="h-3 w-3" /> {t("billing.stripe.secured")}
      </p>
    </form>
  );
}
