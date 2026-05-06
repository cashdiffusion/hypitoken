import { useState } from "react";
import { api } from "@/legacy/lib/api";
import type { Provider } from "@/legacy/lib/types";
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
import { textToModelMap } from "@/legacy/lib/utils";

interface Props {
  provider: Provider;
  onClose: () => void;
  onSaved: () => void;
}

// Per-provider hints so operators don't have to remember which key prefix
// or base URL belongs to which upstream.
const COPY: Record<Provider, { title: string; help: string; placeholder: string; baseDefault: string; routeHint: string }> = {
  anthropic: {
    title: "Add Anthropic API key",
    help: "Anthropic sk-ant-api… key. Stored as JSON in auth_dir, editable from the panel.",
    placeholder: "sk-ant-api03-...",
    baseDefault: "api.anthropic.com",
    routeHint: "{base_url}/v1/messages",
  },
  openai: {
    title: "Add OpenAI / Codex API key",
    help: "OpenAI sk-proj-… or sk-… key. Routed via the Codex endpoint to {base_url}/v1/chat/completions or /v1/responses.",
    placeholder: "sk-proj-...",
    baseDefault: "api.openai.com",
    routeHint: "{base_url}/v1/chat/completions",
  },
};

export function APIKeyModal({ provider, onClose, onSaved }: Props) {
  const copy = COPY[provider];
  const [apiKey, setAPIKey] = useState("");
  const [label, setLabel] = useState("");
  const [proxy, setProxy] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [group, setGroup] = useState("");
  const [modelMapText, setModelMapText] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      const parsed = textToModelMap(modelMapText);
      if (parsed.errors.length > 0) {
        throw new Error("model map: " + parsed.errors.join("; "));
      }
      await api("/admin/api/apikeys", {
        method: "POST",
        body: JSON.stringify({
          provider,
          api_key: apiKey.trim(),
          label: label.trim(),
          proxy_url: proxy.trim(),
          base_url: baseURL.trim(),
          group: group.trim(),
          model_map: parsed.map,
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
          <DialogDescription>{copy.help}</DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label>API key</Label>
          <Input
            type="password"
            autoFocus
            className="mono"
            placeholder={copy.placeholder}
            value={apiKey}
            onChange={(e) => setAPIKey(e.currentTarget.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label>Label</Label>
          <Input
            placeholder="primary"
            value={label}
            onChange={(e) => setLabel(e.currentTarget.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label>Base URL (optional, for relay vendors)</Label>
          <Input
            className="mono"
            placeholder={`https://api.your-relay-vendor.com (default: ${copy.baseDefault})`}
            value={baseURL}
            onChange={(e) => setBaseURL(e.currentTarget.value)}
          />
          <p className="text-xs text-muted-foreground">
            Requests with this key go to <span className="mono">{copy.routeHint}</span>. Leave
            empty for the default upstream.
          </p>
        </div>
        <div className="space-y-1.5">
          <Label>Proxy URL (optional)</Label>
          <Input
            className="mono"
            placeholder="http:// or socks5://"
            value={proxy}
            onChange={(e) => setProxy(e.currentTarget.value)}
          />
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
        <div className="space-y-1.5">
          <Label>Model map (optional)</Label>
          <Textarea
            className="mono text-sm h-32"
            placeholder={
              provider === "openai"
                ? "gpt-5 = vendor-name/gpt-5\ngpt-5-mini ="
                : "claude-opus-4-6 = [0.16]稳定喵/claude-opus-4-6\nclaude-haiku-4-5 ="
            }
            value={modelMapText}
            onChange={(e) => setModelMapText(e.currentTarget.value)}
          />
          <p className="text-xs text-muted-foreground">
            One <span className="mono">client_model = upstream_model</span> per line. When
            non-empty, this key only serves listed client models, and the request body's{" "}
            <span className="mono">model</span> is rewritten before forwarding.
          </p>
        </div>
        {err && <div className="text-sm text-destructive">{err}</div>}
        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || !apiKey.trim()} onClick={save}>
            {busy ? "Saving…" : "Add"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
