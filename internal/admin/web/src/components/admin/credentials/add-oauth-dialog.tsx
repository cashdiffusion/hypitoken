import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { apiPost } from "@/lib/api";
import { errMsg } from "@/lib/utils";

/* Two-step manual OAuth: the server mints the authorize URL (optionally through
 * a proxy, which is then pinned to the credential), the operator completes the
 * consent in a browser that can actually reach the IdP, and pastes the callback
 * back in for the token exchange. */
export function AddOAuthDialog({
  provider,
  onClose,
  onCreated,
}: {
  provider: "anthropic" | "openai" | null;
  onClose: () => void;
  onCreated: () => void;
}) {
  const { t } = useTranslation();
  const [step, setStep] = useState<1 | 2>(1);
  const [proxy, setProxy] = useState("");
  const [label, setLabel] = useState("");
  const [maxC, setMaxC] = useState("5");
  const [group, setGroup] = useState("");
  const [sess, setSess] = useState<{
    session_id: string;
    auth_url: string;
    redirect_uri: string;
  } | null>(null);
  const [callback, setCallback] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    // Reset state when dialog opens with a fresh provider.
    if (provider) {
      setStep(1);
      setProxy("");
      setLabel("");
      setMaxC("5");
      setGroup("");
      setSess(null);
      setCallback("");
    }
  }, [provider]);

  if (!provider) return null;

  const isClaude = provider === "anthropic";
  const title = isClaude ? t("admin.creds.oauth.titleClaude") : t("admin.creds.oauth.titleCodex");
  const intro = isClaude ? t("admin.creds.oauth.introClaude") : t("admin.creds.oauth.introCodex");

  const start = async () => {
    setBusy(true);
    try {
      const r = await apiPost<{ session_id: string; auth_url: string; redirect_uri: string }>(
        "/admin/credentials/oauth/start",
        { provider, proxy_url: proxy, label },
      );
      setSess({ session_id: r.session_id, auth_url: r.auth_url, redirect_uri: r.redirect_uri });
      setStep(2);
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  const copyURL = async () => {
    if (!sess) return;
    try {
      await navigator.clipboard.writeText(sess.auth_url);
      toast.success(t("common.copied"));
    } catch {
      // clipboard unavailable (insecure context) — the field is selectable
    }
  };

  const finish = async () => {
    if (!sess) return;
    setBusy(true);
    try {
      await apiPost("/admin/credentials/oauth/finish", {
        session_id: sess.session_id,
        callback: callback.trim(),
        max_concurrent: Number(maxC) || 0,
        group,
      });
      toast.success(t("admin.creds.oauth.added"));
      onCreated();
      onClose();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={!!provider} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{intro}</DialogDescription>
        </DialogHeader>
        {step === 1 && (
          <div className="grid gap-3 py-2">
            <div className="space-y-2">
              <Label htmlFor="ao-1">{t("admin.creds.oauth.proxyOptional")}</Label>
              <Input
                id="ao-1"
                placeholder={t("admin.creds.edit.proxyPlaceholder")}
                value={proxy}
                onChange={(e) => setProxy(e.target.value)}
                className="font-mono"
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label htmlFor="ao-2">{t("admin.creds.cols.label")}</Label>
                <Input
                  id="ao-2"
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                  placeholder="team-a"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="ao-3">{t("admin.creds.edit.maxConcurrent")}</Label>
                <Input
                  id="ao-3"
                  type="number"
                  min={0}
                  value={maxC}
                  onChange={(e) => setMaxC(e.target.value)}
                />
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="ao-4">{t("admin.creds.cols.group")}</Label>
              <Input
                id="ao-4"
                value={group}
                onChange={(e) => setGroup(e.target.value)}
                placeholder={t("admin.creds.edit.groupPlaceholder")}
              />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>
                {t("common.cancel")}
              </Button>
              <Button disabled={busy} onClick={start}>
                {busy ? t("admin.creds.oauth.generating") : t("admin.creds.oauth.generate")}
              </Button>
            </DialogFooter>
          </div>
        )}
        {step === 2 && sess && (
          <div className="grid gap-3 py-2">
            <p className="text-sm text-muted-foreground">
              <b>1.</b> {t("admin.creds.oauth.step1")}
            </p>
            <div className="space-y-2">
              <Label htmlFor="ao-5">{t("admin.creds.oauth.loginUrl")}</Label>
              <div className="flex gap-2">
                <Input
                  id="ao-5"
                  readOnly
                  onFocus={(e) => e.currentTarget.select()}
                  className="flex-1 bg-muted font-mono text-xs"
                  value={sess.auth_url}
                />
                <Button variant="outline" onClick={copyURL}>
                  {t("common.copy")}
                </Button>
              </div>
            </div>
            <div className="space-y-2 pt-2 text-sm text-muted-foreground">
              <p>
                <b>2.</b>{" "}
                <Trans
                  i18nKey="admin.creds.oauth.step2"
                  values={{ uri: sess.redirect_uri }}
                  components={{ code: <code className="break-all font-mono" /> }}
                />
              </p>
              <p>
                <b>3.</b>{" "}
                <Trans
                  i18nKey="admin.creds.oauth.step3"
                  components={{ code: <code className="font-mono" /> }}
                />
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="ao-6">{t("admin.creds.oauth.callbackLabel")}</Label>
              <Textarea
                id="ao-6"
                className="h-28 font-mono text-xs"
                placeholder={`${sess.redirect_uri}?code=xxx&state=yyy`}
                value={callback}
                onChange={(e) => setCallback(e.target.value)}
              />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setStep(1)}>
                {t("common.back")}
              </Button>
              <Button disabled={busy || !callback.trim()} onClick={finish}>
                {busy ? t("admin.creds.oauth.exchanging") : t("admin.creds.oauth.finish")}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
