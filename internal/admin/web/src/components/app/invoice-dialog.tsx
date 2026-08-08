import { Building2, Check, Copy, Landmark, Loader2, Search } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { apiGet, apiPost } from "@/lib/api";
import type { InvoicePaymentInfo, InvoiceTitle, Ticket } from "@/lib/types";
import { cn, errMsg } from "@/lib/utils";

/** A 统一社会信用代码 is 15–20 chars of uppercase letters and digits. Mirrors the
 *  server's isLikelyTaxNo so the user is told before a round-trip. */
const TAX_NO_RE = /^[0-9A-Z]{15,20}$/;

/** CopyField renders one labelled value with a copy button. The whole point of
 *  the payment panel is that these get pasted into a banking app, where a
 *  mistyped account number is an unrecoverable transfer. */
function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      /* clipboard blocked — the value is on screen to read anyway */
    }
  };
  return (
    <div className="flex items-start justify-between gap-3 border-b border-border/40 py-2 last:border-0">
      <span className="shrink-0 pt-0.5 text-xs text-muted-foreground">{label}</span>
      <div className="flex min-w-0 items-start gap-2">
        <span className="text-right font-mono text-sm break-all">{value}</span>
        <button
          type="button"
          onClick={copy}
          className="mt-0.5 shrink-0 text-muted-foreground transition hover:text-foreground"
          aria-label={label}
        >
          {copied ? (
            <Check className="h-3.5 w-3.5 text-success" />
          ) : (
            <Copy className="h-3.5 w-3.5" />
          )}
        </button>
      </div>
    </div>
  );
}

export function InvoiceDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<InvoiceTitle[]>([]);
  const [searching, setSearching] = useState(false);
  const [picked, setPicked] = useState(false);
  const [taxNo, setTaxNo] = useState("");
  const [amount, setAmount] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [filed, setFiled] = useState<{ ticket: Ticket; payment: InvoicePaymentInfo } | null>(null);
  const debounce = useRef<ReturnType<typeof setTimeout> | null>(null);

  const reset = useCallback(() => {
    setQuery("");
    setResults([]);
    setPicked(false);
    setTaxNo("");
    setAmount("");
    setNote("");
    setFiled(null);
  }, []);

  // Company lookup is debounced: the upstream suggest API is rate-limited per
  // server IP and shared by every customer, so firing on each keystroke would
  // burn the shared budget for one person's typing.
  useEffect(() => {
    if (picked || query.trim().length < 2) {
      setResults([]);
      return;
    }
    if (debounce.current) clearTimeout(debounce.current);
    debounce.current = setTimeout(async () => {
      setSearching(true);
      try {
        const r = await apiGet<{ titles: InvoiceTitle[] }>(
          `/invoice/title-suggest?q=${encodeURIComponent(query.trim())}`,
        );
        setResults(r.titles ?? []);
      } catch {
        // Lookup is a convenience; typing the 抬头 by hand still works.
        setResults([]);
      } finally {
        setSearching(false);
      }
    }, 350);
    return () => {
      if (debounce.current) clearTimeout(debounce.current);
    };
  }, [query, picked]);

  const pick = (title: InvoiceTitle) => {
    setQuery(title.name);
    setTaxNo(title.tax_no ?? "");
    setPicked(true);
    setResults([]);
  };

  const taxNoValid = TAX_NO_RE.test(taxNo.trim().toUpperCase());
  const canSubmit = query.trim().length > 0 && taxNoValid && !busy;

  const submit = async () => {
    if (!canSubmit) return;
    setBusy(true);
    try {
      const r = await apiPost<{ ticket: Ticket; payment: InvoicePaymentInfo }>("/invoice/request", {
        title: { name: query.trim(), tax_no: taxNo.trim().toUpperCase() },
        amount_cny: amount ? Number.parseFloat(amount) : 0,
        note: note.trim(),
      });
      setFiled(r);
    } catch (e) {
      toast.error(errMsg(e, t("invoice.errors.submit")));
    } finally {
      setBusy(false);
    }
  };

  const close = (v: boolean) => {
    if (!v) reset();
    onOpenChange(v);
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-[540px]">
        {filed ? (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <Landmark className="h-5 w-5 text-primary" />
                {t("invoice.paid.title")}
              </DialogTitle>
              <DialogDescription>
                {t("invoice.paid.sub", { id: filed.ticket.id })}
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-2">
              <div className="rounded-xl border border-border-strong bg-muted/30 px-4 py-1">
                <CopyField
                  label={t("invoice.paid.accountName")}
                  value={filed.payment.account_name}
                />
                <CopyField label={t("invoice.paid.accountNo")} value={filed.payment.account_no} />
                <CopyField label={t("invoice.paid.bankBranch")} value={filed.payment.bank_branch} />
                <CopyField label={t("invoice.paid.bankCode")} value={filed.payment.bank_code} />
              </div>
              <p className="text-xs leading-relaxed text-muted-foreground">
                {t("invoice.paid.note")}
              </p>
            </div>
            <DialogFooter>
              <Button onClick={() => close(false)}>{t("invoice.paid.done")}</Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t("invoice.form.title")}</DialogTitle>
              <DialogDescription>{t("invoice.form.sub")}</DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-2">
              <div className="space-y-2">
                <Label htmlFor="company">{t("invoice.form.company")}</Label>
                <div className="relative">
                  <Search className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    id="company"
                    value={query}
                    onChange={(e) => {
                      setQuery(e.target.value);
                      setPicked(false);
                    }}
                    placeholder={t("invoice.form.companyPlaceholder")}
                    className="pl-9"
                    autoComplete="off"
                  />
                  {searching ? (
                    <Loader2 className="absolute top-1/2 right-3 h-4 w-4 -translate-y-1/2 animate-spin text-muted-foreground" />
                  ) : null}
                </div>
                {results.length > 0 ? (
                  <div className="max-h-52 overflow-y-auto rounded-lg border border-border/60 bg-card/60">
                    {results.map((r) => (
                      <button
                        key={`${r.name}-${r.tax_no}`}
                        type="button"
                        onClick={() => pick(r)}
                        className="flex w-full items-start gap-2 border-b border-border/40 px-3 py-2 text-left transition last:border-0 hover:bg-muted/60"
                      >
                        <Building2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                        <div className="min-w-0">
                          <div className="truncate text-sm">{r.name}</div>
                          {r.tax_no ? (
                            <div className="font-mono text-[11px] text-muted-foreground">
                              {r.tax_no}
                            </div>
                          ) : null}
                        </div>
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>

              <div className="space-y-2">
                <Label htmlFor="taxno">{t("invoice.form.taxNo")}</Label>
                <Input
                  id="taxno"
                  value={taxNo}
                  onChange={(e) => setTaxNo(e.target.value.toUpperCase())}
                  placeholder="91440300MA5EYQ8L2K"
                  className={cn("font-mono", taxNo && !taxNoValid && "border-destructive")}
                  autoComplete="off"
                />
                <p className="text-xs text-muted-foreground">
                  {taxNo && !taxNoValid ? (
                    <span className="text-destructive">{t("invoice.form.taxNoInvalid")}</span>
                  ) : (
                    t("invoice.form.taxNoHint")
                  )}
                </p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="amount">{t("invoice.form.amount")}</Label>
                <Input
                  id="amount"
                  type="number"
                  min="0"
                  step="0.01"
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  placeholder="0.00"
                  className="font-mono"
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="note">{t("invoice.form.note")}</Label>
                <Textarea
                  id="note"
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  rows={2}
                  placeholder={t("invoice.form.notePlaceholder")}
                  className="resize-none"
                />
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => close(false)}>
                {t("common.cancel")}
              </Button>
              <Button onClick={submit} disabled={!canSubmit}>
                {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                {t("invoice.form.submit")}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
