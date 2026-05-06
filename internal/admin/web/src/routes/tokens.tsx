import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Plus, RefreshCw, Trash2, Copy, Check, Eye, EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";
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
  const { t: tt } = useTranslation();
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
          <h1 className="font-display text-3xl font-semibold tracking-tight">{tt("tokens.title")}</h1>
          <p className="text-muted-foreground">{tt("tokens.sub")}</p>
        </div>
        <Button onClick={() => setOpen(true)} className="gap-2"><Plus className="h-4 w-4" /> {tt("tokens.newToken")}</Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {tokens.length === 0 ? (
            <div className="p-12 text-center text-sm text-muted-foreground">{tt("tokens.none")}</div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{tt("common.name")}</TableHead>
                  <TableHead>Token</TableHead>
                  <TableHead className="text-right">{tt("tokens.spendingCap")}</TableHead>
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
                    <TableCell className="font-mono tabular-nums text-right">{t.monthly_usd_cap > 0 ? fmtUSD(t.monthly_usd_cap) : tt("common.unlimited")}</TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button size="icon" variant="ghost" className="h-8 w-8" title={tt("tokens.rotate")} onClick={async () => {
                          await apiPost(`/tokens/${t.id}/rotate`);
                          toast.success(tt("tokens.rotated"));
                          refresh();
                        }}>
                          <RefreshCw className="h-3.5 w-3.5" />
                        </Button>
                        <Button size="icon" variant="ghost" className="h-8 w-8 text-destructive hover:bg-destructive/10" onClick={async () => {
                          if (!confirm(tt("tokens.confirmDelete"))) return;
                          await apiDelete(`/tokens/${t.id}`);
                          toast.success(tt("tokens.deleted"));
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
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [cap, setCap] = useState("");

  const submit = async () => {
    try {
      await apiPost("/tokens", {
        name,
        monthly_usd_cap: parseFloat(cap) || 0,
      });
      toast.success(t("tokens.dialog.created"));
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
          <DialogTitle>{t("tokens.dialog.title")}</DialogTitle>
          <DialogDescription>{t("tokens.dialog.sub")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="space-y-2">
            <Label htmlFor="name">{t("common.name")}</Label>
            <Input id="name" placeholder={t("tokens.dialog.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="cap">{t("tokens.dialog.capLabel")}</Label>
            <Input id="cap" type="number" step="0.01" placeholder={t("tokens.dialog.capPlaceholder")} value={cap} onChange={(e) => setCap(e.target.value)} />
            <p className="text-xs text-muted-foreground">{t("tokens.dialog.capHint")}</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("common.cancel")}</Button>
          <Button onClick={submit}>{t("common.create")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
