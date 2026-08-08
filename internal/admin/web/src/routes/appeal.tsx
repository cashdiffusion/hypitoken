import { LifeBuoy, Loader2, MailCheck, ShieldQuestion } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { AppealKeyNotice, TicketStatusBadge, TicketThread } from "@/components/app/ticket-thread";
import { OtpField } from "@/components/auth/otp-field";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import type { Ticket } from "@/lib/types";
import { errMsg } from "@/lib/utils";

// The appeal endpoints are the only ones a disabled account can reach, so they
// take no JWT — which means they cannot go through lib/api's authenticated
// client. These call fetch directly.
const BASE = "/api/v2";

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const msg =
      data && typeof data === "object" && "error" in data && typeof data.error === "string"
        ? data.error
        : `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return data as T;
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path);
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return data as T;
}

// The access key is the submitter's only handle on their thread — they cannot
// log in to look it up — so we mirror it into localStorage the moment it comes
// back, and re-open the thread automatically on a return visit.
const KEY_STORE = "hypitoken.appeal";

type Saved = { id: number; key: string };

function loadSaved(): Saved | null {
  try {
    const raw = localStorage.getItem(KEY_STORE);
    return raw ? (JSON.parse(raw) as Saved) : null;
  } catch {
    return null;
  }
}

export default function AppealPage() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const [email, setEmail] = useState(params.get("email") ?? "");
  const [code, setCode] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [freshKey, setFreshKey] = useState("");

  // Re-open an existing appeal from a previous visit (or from a link carrying
  // ?id=&key=, which is what the notification email can point at).
  useEffect(() => {
    const linked =
      params.get("id") && params.get("key")
        ? { id: Number(params.get("id")), key: params.get("key") as string }
        : loadSaved();
    if (!linked?.id || !linked.key) return;
    void get<{ ticket: Ticket }>(`/appeal/${linked.id}?key=${encodeURIComponent(linked.key)}`)
      .then((r) => setTicket(r.ticket))
      .catch(() => {
        /* stale key — fall through to the blank form */
      });
  }, [params]);

  const sendCode = async () => {
    if (!email.trim() || busy) return;
    setBusy(true);
    try {
      await post("/appeal/send-code", { email: email.trim() });
      setSent(true);
      toast.success(t("support.appeal.codeSent"));
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.sendCode")));
    } finally {
      setBusy(false);
    }
  };

  const submit = async () => {
    if (!email.trim() || code.length !== 6 || !body.trim() || busy) return;
    setBusy(true);
    try {
      const r = await post<{ ticket: Ticket }>("/appeal", {
        email: email.trim(),
        code,
        subject: subject.trim(),
        body: body.trim(),
      });
      const key = r.ticket.access_key ?? "";
      if (key) {
        localStorage.setItem(KEY_STORE, JSON.stringify({ id: r.ticket.id, key }));
        setFreshKey(key);
      }
      setTicket(r.ticket);
      toast.success(t("support.appeal.submitted"));
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.create")));
    } finally {
      setBusy(false);
    }
  };

  const reply = async (text: string) => {
    if (!ticket) return;
    const saved = loadSaved();
    const key = freshKey || saved?.key || "";
    try {
      const r = await post<{ ticket: Ticket }>(
        `/appeal/${ticket.id}/reply?key=${encodeURIComponent(key)}`,
        { body: text },
      );
      setTicket(r.ticket);
    } catch (e) {
      toast.error(errMsg(e, t("support.errors.reply")));
    }
  };

  // ---- existing / just-filed appeal ----
  if (ticket) {
    return (
      <div className="mx-auto w-full max-w-2xl space-y-6 px-4 py-12">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="font-display text-2xl font-semibold tracking-tight">{ticket.subject}</h1>
            <p className="text-xs text-muted-foreground">
              #{ticket.id} · {ticket.email}
            </p>
          </div>
          <TicketStatusBadge status={ticket.status} />
        </div>
        {freshKey ? <AppealKeyNotice id={ticket.id} accessKey={freshKey} /> : null}
        <div className="rounded-2xl border border-border/60 bg-card/40 p-6">
          <TicketThread ticket={ticket} onReply={reply} replyAs="user" />
        </div>
        <p className="text-center text-xs text-muted-foreground">{t("support.appeal.replyNote")}</p>
      </div>
    );
  }

  // ---- appeal form ----
  return (
    <div className="mx-auto w-full max-w-lg space-y-8 px-4 py-12">
      <div className="text-center">
        <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl border border-primary/20 bg-primary/10">
          <ShieldQuestion className="h-6 w-6 text-primary" />
        </div>
        <h1 className="font-display text-3xl font-semibold tracking-tight">
          {t("support.appeal.title")}
        </h1>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          {t("support.appeal.sub")}
        </p>
      </div>

      <div className="space-y-5 rounded-2xl border border-border/60 bg-card/40 p-6">
        <div className="space-y-2">
          <Label htmlFor="email">{t("common.email")}</Label>
          <div className="flex gap-2">
            <Input
              id="email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
              autoComplete="email"
            />
            <Button variant="outline" onClick={sendCode} disabled={!email.trim() || busy}>
              {busy && !sent ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <MailCheck className="h-4 w-4" />
              )}
              {t("support.appeal.getCode")}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">{t("support.appeal.emailHint")}</p>
        </div>

        {sent ? (
          <div className="space-y-2">
            <Label>{t("auth.register.code")}</Label>
            <OtpField value={code} onChange={setCode} />
          </div>
        ) : null}

        <div className="space-y-2">
          <Label htmlFor="subject">{t("support.form.subject")}</Label>
          <Input
            id="subject"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            placeholder={t("support.appeal.subjectPlaceholder")}
            maxLength={200}
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="body">{t("support.appeal.body")}</Label>
          <Textarea
            id="body"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder={t("support.appeal.bodyPlaceholder")}
            rows={6}
            className="resize-none"
          />
          <p className="text-xs text-muted-foreground">{t("support.appeal.bodyHint")}</p>
        </div>

        <Button
          className="w-full"
          onClick={submit}
          disabled={!email.trim() || code.length !== 6 || !body.trim() || busy}
        >
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <LifeBuoy className="h-4 w-4" />}
          {t("support.appeal.submit")}
        </Button>
      </div>
    </div>
  );
}
