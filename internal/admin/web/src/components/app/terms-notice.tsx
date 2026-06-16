import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

// localStorage flag set only when the user ticks "don't show again". Absent →
// the notice shows on every dashboard visit (the intended default).
const DISMISS_KEY = "hypitoken.tos-notice.dismissed";

// TermsNotice is the service-availability / Terms-of-Service announcement shown
// on the dashboard. It re-appears every visit until the user opts out via the
// "don't show again" checkbox, which persists the dismissal locally. Closing it
// any other way (button, ✕, ESC) only dismisses it for this visit.
export function TermsNotice() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(() => {
    try {
      return localStorage.getItem(DISMISS_KEY) !== "1";
    } catch {
      return true;
    }
  });
  const [dontShow, setDontShow] = useState(false);

  const close = () => {
    if (dontShow) {
      try {
        localStorage.setItem(DISMISS_KEY, "1");
      } catch {
        // private mode / storage disabled — fall back to per-visit display
      }
    }
    setOpen(false);
  };

  const linkCls = "text-primary underline-offset-4 hover:underline";

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) close();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("legal.notice.title")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3 text-sm leading-relaxed text-muted-foreground">
          <p>{t("legal.notice.availability")}</p>
          <p>{t("legal.notice.restricted")}</p>
          <p>{t("legal.notice.compliance")}</p>
          <p className="text-xs">
            <Trans i18nKey="legal.notice.links">
              For full details, see our
              <Link to="/terms" className={linkCls}>
                Terms of Service
              </Link>
              and
              <Link to="/privacy" className={linkCls}>
                Privacy Policy
              </Link>
              .
            </Trans>
          </p>
        </div>
        <label
          htmlFor="tos-notice-dontshow"
          className="flex cursor-pointer items-center gap-2 text-sm text-foreground/80"
        >
          <Checkbox
            id="tos-notice-dontshow"
            checked={dontShow}
            onCheckedChange={(v) => setDontShow(v === true)}
          />
          {t("legal.notice.dontShow")}
        </label>
        <DialogFooter>
          <Button onClick={close}>{t("legal.notice.acknowledge")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
