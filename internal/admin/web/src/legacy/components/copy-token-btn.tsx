import { useState } from "react";
import { Button } from "@/components/ui/button";
import { copyToClipboard } from "@/legacy/lib/utils";
import { Check, Copy } from "lucide-react";

export function CopyTokenBtn({ token }: { token: string }) {
  const [copied, setCopied] = useState(false);
  const onClick = async () => {
    try {
      await copyToClipboard(token);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore
    }
  };
  return (
    <Button variant="outline" size="sm" onClick={onClick}>
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      {copied ? "Copied" : "Copy"}
    </Button>
  );
}
