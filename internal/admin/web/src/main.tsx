import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AppErrorBoundary } from "@/components/app-error-boundary";
import { ThemeProvider } from "@/hooks/use-theme";
import App from "./App";
import "./i18n";
import "./styles/globals.css";

// biome-ignore lint/style/noNonNullAssertion: #app is guaranteed by index.html
createRoot(document.getElementById("app")!).render(
  <StrictMode>
    <AppErrorBoundary>
      <ThemeProvider>
        <App />
      </ThemeProvider>
    </AppErrorBoundary>
  </StrictMode>,
);
