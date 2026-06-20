import { Check } from "lucide-react";
import { useEffect, useState } from "react";
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
import { useAuth } from "@/hooks/use-auth";
import { apiPatch } from "@/lib/api";
import type { UserProfile } from "@/lib/types";
import { cn, errMsg } from "@/lib/utils";

// NicknameDialog — edit the public nickname + leaderboard opt-in. On save it
// refreshes the auth user so the dashboard greeting + arena identity update
// everywhere at once.
export function NicknameDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  const { t } = useTranslation();
  const { user, refresh } = useAuth();
  const [name, setName] = useState("");
  const [optIn, setOptIn] = useState(false);
  const [saving, setSaving] = useState(false);

  // Seed the form from the freshest profile each time the dialog opens.
  useEffect(() => {
    if (!open) return;
    setName(user?.display_name || "");
    setOptIn(!!user?.public_opt_in);
  }, [open, user?.display_name, user?.public_opt_in]);

  const valid = name.trim().length >= 2 && name.trim().length <= 24;

  const submit = async () => {
    if (!valid) {
      toast.error(t("profile.invalidNick"));
      return;
    }
    setSaving(true);
    try {
      await apiPatch<UserProfile>("/me/profile", {
        display_name: name.trim(),
        public_opt_in: optIn,
      });
      await refresh();
      toast.success(t("profile.saved"));
      onOpenChange(false);
    } catch (e) {
      toast.error(errMsg(e) || t("profile.failed"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[460px]">
        <DialogHeader>
          <DialogTitle>{t("profile.title")}</DialogTitle>
          <DialogDescription>{t("profile.publicOptInHint")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-5 py-2">
          <div className="space-y-2">
            <Label htmlFor="nick">{t("profile.nickname")}</Label>
            <Input
              id="nick"
              value={name}
              maxLength={24}
              placeholder={t("profile.nicknamePlaceholder")}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && valid && !saving) submit();
              }}
            />
          </div>

          {/* Opt-in toggle — built from a button since there's no Switch in the
              shadcn set here. */}
          <button
            type="button"
            onClick={() => setOptIn((v) => !v)}
            className="flex items-center justify-between gap-4 rounded-xl border border-border/60 p-3 text-left transition-colors hover:bg-primary/[0.03]"
          >
            <div className="min-w-0">
              <div className="text-sm font-medium">{t("profile.publicOptIn")}</div>
            </div>
            <span
              className={cn(
                "relative h-6 w-11 shrink-0 rounded-full transition-colors",
                optIn ? "bg-primary" : "bg-muted",
              )}
            >
              <span
                className={cn(
                  "absolute top-0.5 grid h-5 w-5 place-items-center rounded-full bg-white shadow transition-all",
                  optIn ? "left-[22px]" : "left-0.5",
                )}
              >
                {optIn && <Check className="h-3 w-3 text-primary" />}
              </span>
            </span>
          </button>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("profile.cancel")}
          </Button>
          <Button onClick={submit} disabled={!valid || saving}>
            {saving ? t("profile.saving") : t("profile.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
