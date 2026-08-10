import { useState } from "react";
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
import { apiPost } from "@/lib/api";
import { errMsg } from "@/lib/utils";
import type { CredentialDialogProps } from "./add-apikey-dialog";

/* UploadJSONDialog persists a raw credential JSON (exported from another
 * instance) into the pool. Works for both OAuth and API-key shapes — the
 * backend infers the kind from the JSON's `type` field. */
export function UploadJSONDialog({
  open,
  onOpenChange,
  provider,
  onCreated,
}: CredentialDialogProps & { provider: string }) {
  const { t } = useTranslation();
  const [content, setContent] = useState("");
  const [label, setLabel] = useState("");
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  const [busy, setBusy] = useState(false);
  const reset = () => {
    setContent("");
    setLabel("");
    setProxy("");
    setGroup("");
  };
  const submit = async () => {
    if (!content.trim()) {
      toast.error(t("admin.creds.uploadEmpty"));
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(content);
    } catch (e) {
      toast.error(t("admin.creds.uploadParseError", { msg: errMsg(e) }));
      return;
    }
    setBusy(true);
    try {
      const r = await apiPost<{ label?: string; id?: string }>("/admin/credentials/upload", {
        content: parsed,
        provider,
        label: label.trim(),
        proxy_url: proxy.trim(),
        group: group.trim(),
      });
      toast.success(t("admin.creds.uploaded", { label: r.label || r.id || "" }));
      onCreated();
      onOpenChange(false);
      reset();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>
            {t("admin.creds.uploadTitle", { provider: provider === "openai" ? "Codex" : "Claude" })}
          </DialogTitle>
          <DialogDescription>{t("admin.creds.uploadDesc")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 py-2">
          <div className="space-y-2">
            <Label htmlFor="uj-1">{t("admin.creds.uploadContentLabel")}</Label>
            <Textarea
              id="uj-1"
              value={content}
              onChange={(e) => setContent(e.target.value)}
              placeholder={'{\n  "type": "oauth",\n  "provider": "anthropic",\n  …\n}'}
              className="min-h-[160px] font-mono text-xs"
            />
          </div>
          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-2">
              <Label htmlFor="uj-2">{t("admin.creds.cols.label")}</Label>
              <Input id="uj-2" value={label} onChange={(e) => setLabel(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="uj-3">{t("admin.creds.edit.proxy")}</Label>
              <Input id="uj-3" value={proxy} onChange={(e) => setProxy(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="uj-4">{t("admin.creds.cols.group")}</Label>
              <Input id="uj-4" value={group} onChange={(e) => setGroup(e.target.value)} />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button disabled={busy} onClick={submit}>
            {busy ? t("admin.creds.uploadBusy") : t("admin.creds.uploadConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
