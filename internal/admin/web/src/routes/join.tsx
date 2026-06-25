import { Building2, Check, LogIn, UserPlus } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router-dom";
import { toast } from "sonner";
import { GlassPanel, PageHeader } from "@/components/app/page-primitives";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/hooks/use-auth";
import { apiGet, apiPost } from "@/lib/api";
import { errMsg } from "@/lib/utils";

interface InviteInfo {
  workspace_id: number;
  workspace_name: string;
  email_masked: string;
  for_you: boolean;
  role: string;
  status: string;
}

// /join/:token — workspace invite acceptance landing. Signed-in invitees accept
// in one click; everyone else is routed to register/login (registration
// auto-claims any pending invite for the verified email).
export default function JoinPage() {
  const { token = "" } = useParams();
  const { t } = useTranslation();
  const { user, refresh } = useAuth();
  const navigate = useNavigate();
  const [info, setInfo] = useState<InviteInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!user) {
      setLoading(false);
      return;
    }
    apiGet<InviteInfo>(`/invites/${token}`)
      .then(setInfo)
      .catch((e) => toast.error(errMsg(e)))
      .finally(() => setLoading(false));
  }, [user, token]);

  const accept = async () => {
    setBusy(true);
    try {
      await apiPost(`/invites/${token}/accept`, {});
      toast.success(t("workspace.join.accepted"));
      await refresh?.();
      navigate("/app/workspace");
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto max-w-lg px-4 py-16">
      <PageHeader
        icon={Building2}
        title={t("workspace.join.title")}
        sub={t("workspace.join.sub")}
      />
      <GlassPanel className="mt-6 space-y-5 p-6">
        {!user ? (
          <>
            <p className="text-sm text-muted-foreground">{t("workspace.join.needAuth")}</p>
            <div className="flex gap-3">
              <Button asChild className="flex-1">
                <Link to={`/register?next=/join/${token}`}>
                  <UserPlus className="mr-2 h-4 w-4" />
                  {t("workspace.join.register")}
                </Link>
              </Button>
              <Button asChild variant="outline" className="flex-1">
                <Link to={`/login?next=/join/${token}`}>
                  <LogIn className="mr-2 h-4 w-4" />
                  {t("workspace.join.login")}
                </Link>
              </Button>
            </div>
          </>
        ) : loading ? (
          <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
        ) : !info ? (
          <p className="text-sm text-muted-foreground">{t("workspace.join.notFound")}</p>
        ) : info.status !== "pending" ? (
          <p className="text-sm text-amber-500">
            {t("workspace.join.invalid", { status: info.status })}
          </p>
        ) : !info.for_you ? (
          <p className="text-sm text-amber-500">
            {t("workspace.join.wrongAccount", { email: info.email_masked })}
          </p>
        ) : (
          <>
            <div className="space-y-1">
              <div className="text-lg font-semibold">{info.workspace_name}</div>
              <div className="text-sm text-muted-foreground">
                {t("workspace.join.roleLine", { role: info.role })}
              </div>
            </div>
            <Button onClick={accept} disabled={busy} className="w-full">
              <Check className="mr-2 h-4 w-4" />
              {t("workspace.join.accept")}
            </Button>
          </>
        )}
      </GlassPanel>
    </div>
  );
}
