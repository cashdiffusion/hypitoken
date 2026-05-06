import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Plus, RefreshCw, Trash2, Copy, Check, Eye, EyeOff } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { apiDelete, apiGet, apiPost, apiPatch } from "@/lib/api";
import { copyToClipboard, fmtUSD } from "@/lib/utils";
import type { UserToken } from "@/lib/types";

export default function TokensPage() {
  const [tokens, setTokens] = useState<UserToken[]>([]);
  const [open, setOpen] = useState(false);
  const [reveal, setReveal] = useState<Record<number, boolean>>({});

  const refresh = async () => {
    const r = await apiGet<{ tokens: UserToken[] }>("/tokens");
    setTokens(r.tokens || []);
  };
  useEffect(() => {
    refresh();
  }, []);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="font-display text-3xl font-semibold tracking-tight">API tokens</h1>
          <p className="text-muted-foreground">One token per app — keep them separate, set caps per-token.</p>
        </div>
        <Button onClick={() => setOpen(true)} className="gap-2"><Plus className="h-4 w-4" /> New token</Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {tokens.length === 0 ? (
            <div className="p-12 text-center text-sm text-muted-foreground">No tokens yet.</div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Token</TableHead>
                  <TableHead className="text-right">Spending cap</TableHead>
                  <TableHead></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.map((t) => (
                  <TableRow key={t.id} className={t.disabled ? "opacity-50" : ""}>
                    <TableCell className="font-medium">{t.name || <span className="text-muted-foreground">(unnamed)</span>}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <code className="rounded bg-muted px-2 py-0.5 font-mono text-xs">
                          {reveal[t.id] ? t.token : t.token.slice(0, 12) + "…" + t.token.slice(-4)}
                        </code>
                        <Button size="icon" variant="ghost" className="h-7 w-7" onClick={() => setReveal((r) => ({ ...r, [t.id]: !r[t.id] }))}>
                          {reveal[t.id] ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                        </Button>
                        <CopyBtn text={t.token} />
                      </div>
                    </TableCell>
                    <TableCell className="font-mono tabular-nums text-right">{t.monthly_usd_cap > 0 ? fmtUSD(t.monthly_usd_cap) : "unlimited"}</TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button size="icon" variant="ghost" className="h-8 w-8" title="Rotate" onClick={async () => {
                          await apiPost(`/tokens/${t.id}/rotate`);
                          toast.success("Token rotated");
                          refresh();
                        }}>
                          <RefreshCw className="h-3.5 w-3.5" />
                        </Button>
                        <Button size="icon" variant="ghost" className="h-8 w-8 text-destructive hover:bg-destructive/10" onClick={async () => {
                          if (!confirm("Delete this token?")) return;
                          await apiDelete(`/tokens/${t.id}`);
                          toast.success("Deleted");
                          refresh();
                        }}>
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <CreateTokenDialog open={open} onOpenChange={setOpen} onCreated={refresh} />
    </div>
  );
}

function CopyBtn({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button size="icon" variant="ghost" className="h-7 w-7" onClick={async () => {
      await copyToClipboard(text);
      setDone(true);
      setTimeout(() => setDone(false), 1500);
    }}>
      {done ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
    </Button>
  );
}

function CreateTokenDialog({ open, onOpenChange, onCreated }: any) {
  const [name, setName] = useState("");
  // Single user-facing knob: spending cap (USD). Maps to monthly_usd_cap
  // server-side. Concurrency, RPM and daily-cap stay at zero (use server
  // defaults / admin-managed) — the operator panel can tighten them
  // per-token if needed.
  const [cap, setCap] = useState("");

  const submit = async () => {
    try {
      await apiPost("/tokens", {
        name,
        monthly_usd_cap: parseFloat(cap) || 0,
      });
      toast.success("Token created");
      onCreated();
      onOpenChange(false);
      setName(""); setCap("");
    } catch (e: any) {
      toast.error(e.message);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>New API token</DialogTitle>
          <DialogDescription>You'll see the full token once on the next screen.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="space-y-2">
            <Label htmlFor="name">Name</Label>
            <Input id="name" placeholder="my-app-prod" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="cap">Spending cap (USD)</Label>
            <Input id="cap" type="number" step="0.01" placeholder="0 = unlimited" value={cap} onChange={(e) => setCap(e.target.value)} />
            <p className="text-xs text-muted-foreground">
              Caps the total this token can spend per calendar month. Leave at 0 for no cap.
              Concurrency and RPM use server defaults — contact admin if you need bespoke limits.
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button onClick={submit}>Create</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
