import {
  CheckoutElementsProvider,
  PaymentElement,
  useCheckoutElements,
} from "@stripe/react-stripe-js/checkout";
import { type Appearance, loadStripe, type Stripe } from "@stripe/stripe-js";
import { CreditCard, Loader2, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// loadStripe is expensive (injects the Stripe.js script) and must be called
// once per publishable key — cache the promise across mounts/keys.
const stripeCache: Record<string, Promise<Stripe | null>> = {};
function getStripe(pk: string): Promise<Stripe | null> {
  if (!stripeCache[pk]) stripeCache[pk] = loadStripe(pk);
  return stripeCache[pk];
}

// preloadStripe warms Stripe.js (script download + Stripe's own preconnects to
// js.stripe.com / api.stripe.com) as soon as the top-up dialog opens — i.e.
// while the user is still picking an amount — instead of waiting until the
// order is created and the Checkout Element mounts. From high-latency networks
// (e.g. China → overseas Stripe) this cold-start cost is the bulk of the
// "spinner then blank gap" delay. Idempotent and cached.
export function preloadStripe(pk: string | undefined | null): void {
  if (pk) getStripe(pk);
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

// StripeTopUp mounts the embedded Checkout Element (Checkout Sessions API).
// Stripe owns the sensitive card / Alipay input inside its iframe; we own
// everything around it. Everything is presented and charged in USD — see the
// adaptivePricing note on the options below.
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
  // biome-ignore lint/correctness/useExhaustiveDependencies: dark is an intentional trigger — buildAppearance reads the DOM directly; re-run when dark mode toggles
  const appearance = useMemo(() => buildAppearance(), [dark]);

  return (
    <CheckoutElementsProvider
      stripe={stripePromise}
      options={{
        clientSecret,
        // No adaptivePricing.allowed: the session is created with Adaptive
        // Pricing disabled server-side, so the buyer is always charged in USD
        // and no Currency Selector is rendered. Alipay stays eligible on USD
        // (most of our live Alipay volume already settles that way) and the
        // buyer gets Alipay's own FX instead of Stripe's 2-4% conversion fee.
        // These two must move together — Stripe requires the Currency Selector
        // to be mounted whenever Adaptive Pricing is live on an Elements
        // integration, so re-enabling one means re-enabling the other.
        // (The buyer's email is prefilled server-side via customer_email, so we
        // must NOT also pass defaultValues.email — that conflicts.)
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
  // ready flips when the PaymentElement iframe has actually painted. We keep an
  // opaque loading overlay over the form until then so the spinner never
  // disappears into a blank gap while Stripe's iframe is still booting.
  const [ready, setReady] = useState(false);
  const confirmedRef = useRef(false);

  // Safety net: if Stripe's onReady never fires (network hiccup), reveal the
  // form anyway after a few seconds so the user is never stuck behind a spinner.
  useEffect(() => {
    const id = setTimeout(() => setReady(true), 9000);
    return () => clearTimeout(id);
  }, []);

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
  // Preformatted USD total — Adaptive Pricing is off, so this is the only
  // currency the buyer ever sees.
  const payAmount = checkout.total?.total?.amount ?? "";

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
    <form onSubmit={submit} className={cn("relative space-y-4", !ready && "min-h-[280px]")}>
      {/* Opaque overlay covering the still-booting PaymentElement. Sits above
          the form (which is mounted underneath so the iframe actually loads)
          until onReady fires, then disappears — no blank gap. */}
      {!ready && (
        <div className="absolute inset-0 z-10 grid place-items-center rounded-lg bg-card">
          <div className="flex flex-col items-center gap-2.5">
            <Loader2 className="h-6 w-6 animate-spin text-primary" />
            <span className="text-xs text-muted-foreground">
              {t("billing.stripe.loadingElement")}
            </span>
          </div>
        </div>
      )}
      <PaymentElement options={{ layout: "tabs" }} onReady={() => setReady(true)} />
      {err && <p className="text-sm text-destructive">{err}</p>}
      <Button
        type="submit"
        size="lg"
        className="w-full gap-2"
        disabled={busy || !checkout.canConfirm || !ready}
      >
        {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <CreditCard className="h-4 w-4" />}
        {busy ? t("billing.stripe.processing") : t("billing.stripe.pay", { amount: payAmount })}
      </Button>
      <p className="flex items-center justify-center gap-1.5 text-[11px] text-muted-foreground">
        <ShieldCheck className="h-3 w-3" /> {t("billing.stripe.secured")}
      </p>
    </form>
  );
}
