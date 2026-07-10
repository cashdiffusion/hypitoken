import { Component, type ErrorInfo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";

interface Props {
  children: ReactNode;
}

interface State {
  failed: boolean;
}

// Last-resort containment for render-time failures. Without an error boundary,
// React removes the entire root after an uncaught component error and users see
// only the page background. Keep the recovery UI deliberately dependency-light.
export class AppErrorBoundary extends Component<Props, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Uncaught React render error", error, info.componentStack);
  }

  render() {
    if (this.state.failed) return <AppCrashFallback />;
    return this.props.children;
  }
}

function AppCrashFallback() {
  const { t } = useTranslation();
  return (
    <main className="grid min-h-dvh place-items-center bg-background px-6 text-foreground">
      <section className="glass w-full max-w-md rounded-2xl p-7 text-center">
        <h1 className="font-display text-2xl font-semibold">{t("common.appCrashed")}</h1>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          {t("common.appCrashedHint")}
        </p>
        <Button className="mt-6" onClick={() => window.location.reload()}>
          {t("common.reload")}
        </Button>
      </section>
    </main>
  );
}
