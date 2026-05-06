import { useState, useRef } from "react";
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

interface Props {
  provider: Provider;
  onClose: () => void;
  onSaved: () => void;
}

// Per-provider UX copy. Matches the sign-in and API-key modals in tone so
// the three buttons on each tab feel like one family.
const COPY: Record<Provider, {
  title: string;
  help: string;
  placeholder: string;
  fileHint: string;
}> = {
  anthropic: {
    title: "Upload Claude credential",
    help: "Paste or select a JSON file. Accepts OAuth credentials from `claude setup-token` / the vendor Claude CLI, or an Anthropic API-key wrapper.",
    placeholder: '{\n  "type": "claude",\n  "access_token": "...",\n  "refresh_token": "...",\n  "email": "you@example.com",\n  "expired": "2026-06-01T00:00:00Z"\n}',
    fileHint: "typical filename: claude-<email>.json",
  },
  openai: {
    title: "Upload Codex credential",
    help: "Paste or select a JSON file. Accepts OAuth credentials from the ChatGPT Codex CLI, or an OpenAI API-key wrapper.",
    placeholder: '{\n  "type": "codex",\n  "access_token": "...",\n  "refresh_token": "...",\n  "id_token": "...",\n  "email": "you@example.com",\n  "account_id": "...",\n  "plan_type": "pro",\n  "expired": "2026-06-01T00:00:00Z"\n}',
    fileHint: "typical filename: codex-<email>[-<plan>].json",
  },
};

export function UploadModal({ provider, onClose, onSaved }: Props) {
  const copy = COPY[provider];
  const [filename, setFilename] = useState("");
  const [content, setContent] = useState("");
  const [label, setLabel] = useState("");
  const [maxC, setMaxC] = useState("5");
  const [proxy, setProxy] = useState("");
  const [group, setGroup] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.currentTarget.files?.[0];
    if (!f) return;
    setFilename(f.name);
    setContent(await f.text());
  };

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      // Parse locally so we can surface JSON errors immediately instead of
      // round-tripping an opaque server 400.
      let parsed: unknown;
      try {
        parsed = JSON.parse(content);
      } catch (x: any) {
        throw new Error("invalid JSON: " + (x?.message || String(x)));
      }
      // Client-side provider mismatch check — matches the server guard but
      // gives a friendlier message without the round-trip.
      if (parsed && typeof parsed === "object") {
        const declared = (parsed as Record<string, unknown>).provider;
        if (typeof declared === "string" && declared && declared !== provider) {
          throw new Error(
            `this file declares provider="${declared}" — switch to the matching tab to upload it`,
          );
        }
      }
      await api("/admin/api/auths/upload", {
        method: "POST",
        body: JSON.stringify({
          provider,
          filename,
          content: parsed,
          label,
          max_concurrent: Number(maxC),
          proxy_url: proxy,
          group,
        }),
      });
      onSaved();
    } catch (x: any) {
      setErr(x.message || String(x));
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
        <input
          type="file"
          accept=".json,application/json"
          ref={fileInputRef}
          onChange={onFile}
          className="hidden"
        />
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => fileInputRef.current?.click()}>
            Choose JSON file…
          </Button>
          <Input
            className="flex-1 mono"
            placeholder={copy.fileHint}
            value={filename}
            onChange={(e) => setFilename(e.currentTarget.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label>or paste JSON</Label>
          <Textarea
            className="mono h-40 text-sm"
            placeholder={copy.placeholder}
            value={content}
            onChange={(e) => setContent(e.currentTarget.value)}
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label>Label</Label>
            <Input value={label} onChange={(e) => setLabel(e.currentTarget.value)} />
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
        {err && <div className="text-sm text-destructive whitespace-pre-wrap">{err}</div>}
        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || !content} onClick={save}>
            {busy ? "Uploading…" : "Add"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
