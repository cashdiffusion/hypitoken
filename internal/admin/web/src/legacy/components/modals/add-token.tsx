import { useState } from "react";
import { api } from "@/legacy/lib/api";
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
import { copyToClipboard, generateSkToken } from "@/legacy/lib/utils";

interface Props {
  onClose: () => void;
  onSaved: () => void;
}

export function AddTokenModal({ onClose, onSaved }: Props) {
  const [name, setName] = useState("");
  const [token, setTokenValue] = useState(() => generateSkToken());
  const [weekly, setWeekly] = useState("");
  const [group, setGroup] = useState("");
  const [created, setCreated] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState(false);

  const regen = () => {
    setTokenValue(generateSkToken());
    setCopied(false);
  };

  const copy = async () => {
    try {
      await copyToClipboard(created || token);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  };

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      const body: Record<string, unknown> = {
        token: token.trim(),
        name: name.trim(),
        group: group.trim(),
      };
      const w = parseFloat(weekly);
      if (!isNaN(w) && w > 0) body.weekly_usd = w;
      const d = await api<{ token: string }>("/admin/api/tokens", {
        method: "POST",
        body: JSON.stringify(body),
      });
      setCreated(d.token);
    } catch (x: any) {
      setErr(x.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{created ? "Token created" : "New client token"}</DialogTitle>
          {created && (
            <DialogDescription>
              Save this token now — you won't see it again in full. Clients send it as{" "}
              <span className="mono text-xs bg-muted px-1.5 py-0.5 rounded">
                Authorization: Bearer &lt;token&gt;
              </span>
              .
            </DialogDescription>
          )}
        </DialogHeader>
        {created ? (
          <div className="space-y-4">
            <div className="bg-muted border rounded-lg px-4 py-3 mono text-sm break-all select-all">
              {created}
            </div>
            <DialogFooter className="gap-2 sm:gap-2 justify-between sm:justify-between">
              <Button variant="outline" onClick={copy}>
                {copied ? "Copied ✓" : "Copy to clipboard"}
              </Button>
              <Button onClick={onSaved}>Done</Button>
            </DialogFooter>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label>Name</Label>
              <Input
                placeholder="e.g. alice-laptop"
                value={name}
                onChange={(e) => setName(e.currentTarget.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Token</Label>
              <div className="flex gap-2">
                <Input
                  className="flex-1 mono text-sm"
                  value={token}
                  onChange={(e) => setTokenValue(e.currentTarget.value)}
                />
                <Button variant="outline" onClick={regen}>
                  Regenerate
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                Format: <code className="mono">sk-</code> + 48 alphanumerics. Paste an existing value to import.
              </p>
            </div>
            <div className="space-y-1.5">
              <Label>Weekly USD limit</Label>
              <Input
                type="number"
                min={0}
                step={0.01}
                className="mono text-sm"
                placeholder="0 = unlimited"
                value={weekly}
                onChange={(e) => setWeekly(e.currentTarget.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>Group</Label>
              <Input
                list="groups-datalist"
                placeholder="public (shared pool)"
                value={group}
                onChange={(e) => setGroup(e.currentTarget.value)}
              />
              <p className="text-xs text-muted-foreground">
                Binds this client to a named credential group. Traffic first tries that group's credentials,
                falling back to public if they're saturated or unhealthy.
              </p>
            </div>
            {err && <div className="text-sm text-destructive">{err}</div>}
            <DialogFooter className="gap-2 sm:gap-2">
              <Button variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button disabled={busy || !token.trim()} onClick={save}>
                {busy ? "Creating…" : "Create"}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
