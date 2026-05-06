import { useState } from "react";
import { api } from "@/legacy/lib/api";
import type { OAuthStart, Provider } from "@/legacy/lib/types";
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
import { copyToClipboard } from "@/legacy/lib/utils";

interface Props {
  provider: Provider;
  onClose: () => void;
  onSaved: () => void;
}

// Per-provider UI copy. The backend handles the actual flow differences
// (auth URL, redirect URI, token endpoint) via the `provider` field on
// /oauth/start — the UI just needs to explain what the user is about to
// see in the browser.
const COPY: Record<Provider, { title: string; intro: string; proxyHint: string; redirect: string; primary: string }> = {
  anthropic: {
    title: "Sign in with Claude",
    intro: "We'll open Claude's OAuth page. If this server can't reach claude.ai / api.anthropic.com directly, set a proxy URL — it's used for the token exchange and every subsequent request with this credential.",
    proxyHint: "Typically only needed on restricted networks",
    redirect: "http://localhost:54545/callback",
    primary: "Open Claude login",
  },
  openai: {
    title: "Sign in with ChatGPT (Codex)",
    intro: "We'll open the ChatGPT Codex OAuth page. If this server can't reach auth.openai.com / chatgpt.com directly, set a proxy URL — it's used for the token exchange and every subsequent request with this credential.",
    proxyHint: "Typically only needed on restricted networks",
    redirect: "http://localhost:1455/auth/callback",
    primary: "Open ChatGPT login",
  },
};

export function OAuthModal({ provider, onClose, onSaved }: Props) {
  const copy = COPY[provider];
  const [step, setStep] = useState<1 | 2>(1);
  const [proxy, setProxy] = useState("");
  const [label, setLabel] = useState("");
  const [maxC, setMaxC] = useState("5");
  const [group, setGroup] = useState("");
  const [sess, setSess] = useState<OAuthStart | null>(null);
  const [callback, setCallback] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState(false);

  const start = async () => {
    setBusy(true);
    setErr("");
    try {
      const d = await api<OAuthStart>("/admin/api/oauth/start", {
        method: "POST",
        body: JSON.stringify({ provider, proxy_url: proxy, label }),
      });
      setSess(d);
      setStep(2);
    } catch (x: any) {
      setErr(x.message);
    } finally {
      setBusy(false);
    }
  };

  const copyUrl = async () => {
    if (!sess) return;
    try {
      await copyToClipboard(sess.auth_url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore
    }
  };

  const finish = async () => {
    if (!sess) return;
    setBusy(true);
    setErr("");
    try {
      await api("/admin/api/oauth/finish", {
        method: "POST",
        body: JSON.stringify({
          session_id: sess.session_id,
          callback: callback.trim(),
          max_concurrent: Number(maxC),
          group,
        }),
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
          <DialogTitle>{copy.title}</DialogTitle>
        </DialogHeader>
        {step === 1 && (
          <>
            <p className="text-base text-muted-foreground">{copy.intro}</p>
            <div className="space-y-1.5">
              <Label>Proxy URL (optional)</Label>
              <Input
                className="mono"
                placeholder="http:// or socks5://"
                value={proxy}
                onChange={(e) => setProxy(e.currentTarget.value)}
              />
              <div className="text-xs text-muted-foreground">{copy.proxyHint}</div>
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
            {err && <div className="text-sm text-destructive">{err}</div>}
            <DialogFooter className="gap-2 sm:gap-2">
              <Button variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button disabled={busy} onClick={start}>
                {busy ? "Starting…" : copy.primary}
              </Button>
            </DialogFooter>
          </>
        )}

        {step === 2 && sess && (
          <>
            <div className="text-base text-muted-foreground space-y-2">
              <p>
                <b>Step 1.</b> Copy the login URL below and open it in a browser where you can sign
                in.
              </p>
            </div>
            <div className="space-y-1.5">
              <Label>Login URL</Label>
              <div className="flex gap-2">
                <Input
                  readOnly
                  onFocus={(e) => e.currentTarget.select()}
                  className="flex-1 mono text-sm bg-muted"
                  value={sess.auth_url}
                />
                <Button variant="outline" onClick={copyUrl}>
                  {copied ? "Copied ✓" : "Copy"}
                </Button>
              </div>
            </div>

            <div className="text-base text-muted-foreground space-y-2 pt-2">
              <p>
                <b>Step 2.</b> After you authorize, the browser redirects to{" "}
                <code className="mono break-all">{copy.redirect}?code=…&amp;state=…</code>. That
                page usually fails to load — <b>that's fine</b>.
              </p>
              <p>
                <b>Step 3.</b> Copy the <b>full URL from the browser address bar</b> (or the{" "}
                <code className="mono">code#state</code> value shown on a manual-copy page) and
                paste it below.
              </p>
            </div>
            <div className="space-y-1.5">
              <Label>Callback URL or code#state</Label>
              <Textarea
                className="mono text-sm h-28"
                placeholder={`${copy.redirect}?code=xxxxx&state=yyyyy`}
                value={callback}
                onChange={(e) => setCallback(e.currentTarget.value)}
              />
            </div>
            {err && <div className="text-sm text-destructive whitespace-pre-wrap">{err}</div>}
            <DialogFooter className="gap-2 sm:gap-2">
              <Button variant="outline" onClick={() => setStep(1)}>
                Back
              </Button>
              <Button disabled={busy || !callback.trim()} onClick={finish}>
                {busy ? "Exchanging…" : "Finish"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
