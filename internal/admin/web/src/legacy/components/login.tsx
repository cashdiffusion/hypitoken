import { useState, type FormEvent } from "react";
import { api, setToken } from "@/legacy/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ThemeToggle } from "@/components/theme-toggle";
import { ArrowRight, KeyRound } from "lucide-react";

export function Login({ onOk }: { onOk: () => void }) {
  const [val, setVal] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    setBusy(true);
    setToken(val.trim());
    try {
      await api("/admin/api/summary");
      onOk();
    } catch (x: any) {
      setToken("");
      setErr(x.message || "auth failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="relative min-h-screen w-full overflow-hidden">
      {/* Atmospheric backdrop: color-mix radial glows that shift with theme */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 opacity-80 [mask-image:radial-gradient(ellipse_at_center,black_40%,transparent_80%)]"
        style={{
          backgroundImage:
            "radial-gradient(60% 50% at 20% 20%, color-mix(in oklch, var(--primary) 18%, transparent), transparent 60%), radial-gradient(50% 40% at 80% 80%, color-mix(in oklch, var(--info) 16%, transparent), transparent 60%)",
        }}
      />

      <div className="relative grid min-h-screen grid-cols-1 lg:grid-cols-[1.2fr_1fr]">
        {/* Left column: editorial headline + ambient info */}
        <aside className="relative hidden lg:flex flex-col justify-between p-10 xl:p-14 border-r border-border">
          <header className="flex items-center justify-between">
            <div className="flex items-center gap-2.5 eyebrow">
              <span className="inline-block h-2 w-2 rounded-full bg-primary status-pulse text-primary" />
              <span>CPA · Claude / Control Console</span>
            </div>
            <ThemeToggle />
          </header>

          <div className="space-y-8 max-w-xl">
            <div className="eyebrow">Operator access · v1</div>
            <h1 className="font-display text-6xl xl:text-7xl leading-[0.92] tracking-tight">
              A quiet console <br />
              for a <span className="text-primary">loud</span> pipeline.
            </h1>
            <p className="text-lg text-muted-foreground max-w-md leading-relaxed">
              Credentials, client tokens, and real-time request ledgers — one reverse-proxy panel,
              authenticated by a single admin token.
            </p>
          </div>

          <footer className="flex items-end justify-between font-mono text-xs text-muted-foreground">
            <dl className="grid grid-cols-3 gap-x-8 gap-y-1 uppercase tracking-wider">
              <dt>Runtime</dt>
              <dt>Transport</dt>
              <dt>Store</dt>
              <dd className="text-foreground">Go 1.24</dd>
              <dd className="text-foreground">HTTP/1.1 · SSE</dd>
              <dd className="text-foreground">Embed FS</dd>
            </dl>
            <div className="text-right tabular uppercase tracking-wider">
              <div>Build</div>
              <div className="text-foreground">SPA · Vite / bun</div>
            </div>
          </footer>
        </aside>

        {/* Right column: the actual auth form */}
        <section className="flex flex-col justify-center px-6 py-12 lg:px-12 xl:px-16">
          <div className="lg:hidden flex items-center justify-between mb-10">
            <div className="eyebrow flex items-center gap-2.5">
              <span className="inline-block h-2 w-2 rounded-full bg-primary" />
              CPA · Claude
            </div>
            <ThemeToggle />
          </div>

          <div className="w-full max-w-md mx-auto stagger">
            <div className="eyebrow mb-3">§01 · Authentication</div>
            <h2 className="font-display text-4xl sm:text-5xl leading-[1.05] tracking-tight mb-2">
              Sign in to continue.
            </h2>
            <p className="text-muted-foreground text-sm mb-10 max-w-sm">
              Paste the admin token configured under <span className="mono">admin_token</span> in
              your server's <span className="mono">config.yaml</span>.
            </p>

            <form onSubmit={submit} className="space-y-5">
              <label htmlFor="admintoken" className="block space-y-2">
                <span className="eyebrow">Admin token</span>
                <div className="relative">
                  <KeyRound className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
                  <Input
                    id="admintoken"
                    type="password"
                    autoFocus
                    spellCheck={false}
                    autoComplete="off"
                    placeholder="••••••••••••••••••••••"
                    value={val}
                    onChange={(e) => setVal(e.currentTarget.value)}
                    className="h-12 pl-10 pr-3 text-base font-mono tracking-wider bg-card border-border-strong focus-visible:ring-primary/50 focus-visible:border-primary"
                  />
                </div>
              </label>

              {err && (
                <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive animate-in fade-in slide-in-from-top-1">
                  {err}
                </div>
              )}

              <Button
                type="submit"
                disabled={busy || !val.trim()}
                className="w-full h-12 text-base group tracking-wide"
              >
                <span>{busy ? "Verifying" : "Continue"}</span>
                <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-0.5" />
              </Button>
            </form>

            <div className="mt-12 pt-6 border-t border-border text-xs text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1">
              <span className="eyebrow">Status</span>
              <span className="mono">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-primary mr-2 align-middle" />
                panel online
              </span>
              <span className="mono text-[10px] ml-auto opacity-60">rev.1.0.0</span>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
