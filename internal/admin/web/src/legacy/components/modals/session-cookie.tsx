import { useState } from "react";
import { api } from "@/legacy/lib/api";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

interface Props {
  onClose: () => void;
  onSaved: () => void;
}

// Drive Claude.com OAuth authorize on the server using a user-supplied
// `sessionKey=sk-ant-sid02-…` cookie. Proxy is mandatory and uTLS is
// forced on the backend — the form just collects the inputs.
export function SessionCookieModal({ onClose, onSaved }: Props) {
  const [cookie, setCookie] = useState("");
  const [proxy, setProxy] = useState("");
  const [label, setLabel] = useState("");
  const [maxC, setMaxC] = useState("5");
  const [group, setGroup] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const submit = async () => {
    setBusy(true);
    setErr("");
    try {
      await api("/admin/api/oauth/session-cookie", {
        method: "POST",
        body: JSON.stringify({
          session_cookie: cookie.trim(),
          proxy_url: proxy.trim(),
          label: label.trim(),
          group: group.trim(),
          max_concurrent: Number(maxC),
        }),
      });
      onSaved();
    } catch (x: any) {
      setErr(x.message);
    } finally {
      setBusy(false);
    }
  };

  const canSubmit =
    !busy && cookie.trim().startsWith("sk-ant-sid") && proxy.trim() !== "";

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Sign in with session cookie</DialogTitle>
        </DialogHeader>
        <div className="text-sm text-muted-foreground space-y-2">
          <p>
            Paste a <code className="mono">sessionKey</code> cookie from a
            browser already signed in to <code className="mono">claude.com</code>.
            The server will drive the OAuth authorize flow on your behalf
            (uTLS Chrome fingerprint, through the proxy you provide) and
            persist the resulting OAuth credential.
          </p>
          <p>
            <b>Proxy is required.</b> Driving claude.com from a server IP
            without one will fail Cloudflare's checks and risks the
            underlying account.
          </p>
        </div>

        <div className="space-y-1.5">
          <Label>sessionKey cookie *</Label>
          <Textarea
            className="mono text-sm h-24"
            placeholder="sk-ant-sid02-…"
            value={cookie}
            onChange={(e) => setCookie(e.currentTarget.value)}
          />
          <div className="text-xs text-muted-foreground">
            DevTools → Application → Cookies → claude.com →{" "}
            <code className="mono">sessionKey</code>
          </div>
        </div>

        <div className="space-y-1.5">
          <Label>Proxy URL *</Label>
          <Input
            className="mono"
            placeholder="http://… or socks5://…"
            value={proxy}
            onChange={(e) => setProxy(e.currentTarget.value)}
          />
          <div className="text-xs text-muted-foreground">
            Use the same egress IP the cookie was issued from when possible.
          </div>
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label>Label</Label>
            <Input
              placeholder="team-a / alice / …"
              value={label}
              onChange={(e) => setLabel(e.currentTarget.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Max concurrent</Label>
            <Input
              type="number"
              min={0}
              value={maxC}
              onChange={(e) => setMaxC(e.currentTarget.value)}
            />
          </div>
        </div>

        <div className="space-y-1.5">
          <Label>Group (optional)</Label>
          <Input
            list="groups-datalist"
            placeholder="public"
            value={group}
            onChange={(e) => setGroup(e.currentTarget.value)}
          />
        </div>

        {err && (
          <div className="text-sm text-destructive whitespace-pre-wrap">
            {err}
          </div>
        )}

        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!canSubmit} onClick={submit}>
            {busy ? "Authorizing…" : "Sign in"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
