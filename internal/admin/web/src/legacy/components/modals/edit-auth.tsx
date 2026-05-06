import { useState } from "react";
import { api } from "@/legacy/lib/api";
import type { AuthRow } from "@/legacy/lib/types";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Checkbox } from "@/components/ui/checkbox";
import { modelMapToText, textToModelMap } from "@/legacy/lib/utils";

interface Props {
  auth: AuthRow;
  onClose: () => void;
  onSaved: () => void;
}

export function EditAuthModal({ auth, onClose, onSaved }: Props) {
  const [disabled, setDisabled] = useState(auth.disabled);
  const [maxC, setMaxC] = useState(String(auth.max_concurrent || 0));
  const [proxy, setProxy] = useState(auth.proxy_url || "");
  const [baseURL, setBaseURL] = useState(auth.base_url || "");
  const [label, setLabel] = useState(auth.label || "");
  const [group, setGroup] = useState(auth.group || "");
  const [modelMapText, setModelMapText] = useState(modelMapToText(auth.model_map));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const isApiKey = auth.kind === "apikey";

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      const body: Record<string, unknown> = { disabled, proxy_url: proxy, label, group };
      if (!isApiKey) body.max_concurrent = Number(maxC);
      if (isApiKey) {
        body.base_url = baseURL;
        const parsed = textToModelMap(modelMapText);
        if (parsed.errors.length > 0) {
          throw new Error("model map: " + parsed.errors.join("; "));
        }
        body.model_map = parsed.map;
      }
      await api(`/admin/api/auths/${encodeURIComponent(auth.id)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      });
      onSaved();
    } catch (x: any) {
      setErr(x.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit credential</DialogTitle>
          <DialogDescription className="mono text-xs">{auth.id}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label>Label</Label>
            <Input value={label} onChange={(e) => setLabel(e.currentTarget.value)} />
          </div>
          {!isApiKey && (
            <div className="space-y-1.5">
              <Label>Max concurrent sessions</Label>
              <Input
                type="number"
                min={0}
                value={maxC}
                onChange={(e) => setMaxC(e.currentTarget.value)}
              />
              <p className="text-xs text-muted-foreground">0 = unlimited</p>
            </div>
          )}
          {isApiKey && (
            <>
              <div className="space-y-1.5">
                <Label>Base URL</Label>
                <Input
                  className="mono"
                  placeholder="https://api.your-relay-vendor.com (default: api.anthropic.com)"
                  value={baseURL}
                  onChange={(e) => setBaseURL(e.currentTarget.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Per-key upstream override; leave blank for the provider default.
                </p>
              </div>
              <div className="space-y-1.5">
                <Label>Model map (optional)</Label>
                <Textarea
                  className="mono text-sm h-32"
                  placeholder={"claude-opus-4-6 = [0.16]稳定喵/claude-opus-4-6\nclaude-haiku-4-5 ="}
                  value={modelMapText}
                  onChange={(e) => setModelMapText(e.currentTarget.value)}
                />
                <p className="text-xs text-muted-foreground">
                  One <span className="mono">client_model = upstream_model</span> per line. When non-empty,
                  this key only serves listed client models, and the request body's{" "}
                  <span className="mono">model</span> is rewritten before forwarding. Leave the right side
                  blank to accept the model without rewriting.
                </p>
              </div>
            </>
          )}
          <div className="space-y-1.5">
            <Label>Proxy URL</Label>
            <Input
              className="mono"
              placeholder="http://host:port or socks5://host:port"
              value={proxy}
              onChange={(e) => setProxy(e.currentTarget.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Group</Label>
            <Input
              list="groups-datalist"
              placeholder="public (shared with everyone)"
              value={group}
              onChange={(e) => setGroup(e.currentTarget.value)}
            />
            <p className="text-xs text-muted-foreground">
              Empty or "public" = shared pool. Name a group to restrict this credential.
              Built-in <span className="font-semibold tracking-wider text-amber-600 dark:text-amber-400">NEW</span>: shared by everyone but idle 10 random whole hours / day.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="disabled"
              checked={disabled}
              onCheckedChange={(v) => setDisabled(!!v)}
            />
            <Label htmlFor="disabled">Disabled</Label>
          </div>
          {err && <div className="text-sm text-destructive">{err}</div>}
        </div>
        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy} onClick={save}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
