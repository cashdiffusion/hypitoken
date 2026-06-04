import { useEffect, useMemo, useRef, useState } from "react";
import { loadStripe, type Stripe, type Appearance } from "@stripe/stripe-js";
import {
  CheckoutElementsProvider,
  PaymentElement,
  CurrencySelectorElement,
  useCheckoutElements,
} from "@stripe/react-stripe-js/checkout";
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

// cssVarToHex resolves a CSS custom property to a Stripe-safe rgb() color. Our
// theme is authored in oklch(); the Stripe appearance API silently REJECTS oklch
// (and other CSS Color-4 functions) and falls back to its default theme — the
// cause of the Element not matching our dark theme. Reading back the canvas
// fillStyle string isn't enough: modern Chrome's canvas now *accepts* oklch and
// echoes it back unchanged, so it would still leak oklch to Stripe. Instead we
// paint a pixel and read it via getImageData, which always rasterizes to real
// RGB regardless of the input color space.
function cssVarToHex(name: string, fallback: string): string {
  if (typeof document === "undefined") return fallback;
  try {
    const probe = document.createElement("span");
    probe.style.color = `var(${name})`;
    probe.style.display = "none";
    document.body.appendChild(probe);
    const computed = getComputedStyle(probe).color; // may be rgb() or oklch()/color()
    probe.remove();
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });
    if (!ctx) return fallback;
    ctx.fillStyle = computed;
    ctx.fillRect(0, 0, 1, 1);
    const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
    return `rgb(${r}, ${g}, ${b})`;
  } catch {
    return fallback;
  }
}

function isDark(): boolean {
  return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
}

// buildAppearance themes the Checkout Element to match the app. We use Stripe's
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
      ".ToggleItem:hover": { border: `1px solid ${primary}` },
    },
  };
}

interface StripeTopUpProps {
  publishableKey: string;
  /** Checkout Session client_secret from the backend topup response. */
  clientSecret: string;
  /** called once Checkout reports the session is settling/settled (in-page
   *  methods like cards) — the parent then polls the backend order until paid.
   *  Redirect methods (Alipay/WeChat) navigate away and resume via return_url. */
  onConfirmed: () => void;
}

// StripeTopUp mounts the embedded Checkout Element (Checkout Sessions API with
// Adaptive Pricing). Stripe owns the sensitive card / Alipay / WeChat input
// inside its iframe; we own everything around it. The Currency Selector lets the
// buyer pay in their localized currency, which is what unlocks Alipay et al.
export function StripeTopUp({ publishableKey, clientSecret, onConfirmed }: StripeTopUpProps) {
  const stripePromise = useMemo(() => getStripe(publishableKey), [publishableKey]);
  // Track the light/dark class on <html> so the Element re-themes live when the
  // user flips the theme toggle while the dialog is open.
  const [dark, setDark] = useState(isDark());
  useEffect(() => {
    const el = document.documentElement;
    const obs = new MutationObserver(() => setDark(el.classList.contains("dark")));
    obs.observe(el, { attributes: true, attributeFilter: ["class"] });
    return () => obs.disconnect();
  }, []);
  const appearance = useMemo(() => buildAppearance(), [dark]);

  return (
    <CheckoutElementsProvider
      stripe={stripePromise}
      options={{
        clientSecret,
        // Mark our integration ready for Adaptive Pricing (we render the
        // mandatory Currency Selector below). Stripe then localizes the
        // presentment currency from the buyer's IP and unlocks local rails.
        // (The buyer's email is prefilled server-side via customer_email, so we
        // must NOT also pass defaultValues.email — that conflicts.)
        adaptivePricing: { allowed: true },
        elementsOptions: { appearance, loader: "auto" },
      }}
    >
      <CheckoutForm onConfirmed={onConfirmed} />
    </CheckoutElementsProvider>
  );
}

function CheckoutForm({ onConfirmed }: { onConfirmed: () => void }) {
  const { t } = useTranslation();
  const result = useCheckoutElements();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const confirmedRef = useRef(false);

  if (result.type === "loading") {
    return (
      <div className="flex items-center justify-center py-10">
        <Loader2 className="h-6 w-6 animate-spin text-primary" />
      </div>
    );
  }
  if (result.type === "error") {
    return <p className="py-6 text-center text-sm text-destructive">{result.error.message}</p>;
  }

  const checkout = result.checkout;
  // Preformatted total in whatever currency the buyer is being charged — USD by
  // default, or the localized currency once Adaptive Pricing kicks in.
  const payAmount = checkout.total?.total?.amount ?? "";
  const hasCurrencyOptions = (checkout.currencyOptions?.length ?? 0) > 0;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    // redirect:if_required keeps cards in-page; Alipay/WeChat bounce out to a
    // hosted auth page and return via the session's return_url.
    const res = await checkout.confirm({ redirect: "if_required" });
    if (res.type === "error") {
      setErr(res.error.message || t("billing.stripe.failed"));
      setBusy(false);
      return;
    }
    // Settled (or processing) in-page — start polling the backend order.
    if (!confirmedRef.current) {
      confirmedRef.current = true;
      onConfirmed();
    }
    setBusy(false);
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      {hasCurrencyOptions && (
        <div className="rounded-md border border-border-strong bg-muted/30 p-2">
          <CurrencySelectorElement />
        </div>
      )}
      <PaymentElement options={{ layout: "tabs" }} />
      {err && <p className="text-sm text-destructive">{err}</p>}
      <Button type="submit" size="lg" className="w-full gap-2" disabled={busy || !checkout.canConfirm}>
        {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <CreditCard className="h-4 w-4" />}
        {busy ? t("billing.stripe.processing") : t("billing.stripe.pay", { amount: payAmount })}
      </Button>
      <p className="flex items-center justify-center gap-1.5 text-[11px] text-muted-foreground">
        <ShieldCheck className="h-3 w-3" /> {t("billing.stripe.secured")}
      </p>
    </form>
  );
}
