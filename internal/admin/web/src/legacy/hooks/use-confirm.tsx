import * as React from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";

interface ConfirmOpts {
  title?: string;
  message?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
}

type Resolver = (ok: boolean) => void;

let push: ((o: ConfirmOpts, r: Resolver) => void) | null = null;

export function confirmDialog(opts: ConfirmOpts = {}): Promise<boolean> {
  return new Promise((resolve) => {
    if (!push) {
      resolve(window.confirm(opts.message || opts.title || ""));
      return;
    }
    push(opts, resolve);
  });
}

export function ConfirmHost() {
  const [stack, setStack] = React.useState<Array<ConfirmOpts & { resolve: Resolver; id: number }>>(
    [],
  );
  const idRef = React.useRef(0);

  React.useEffect(() => {
    push = (o, r) => {
      idRef.current += 1;
      setStack((s) => [...s, { ...o, resolve: r, id: idRef.current }]);
    };
    return () => {
      push = null;
    };
  }, []);

  const top = stack[stack.length - 1];
  const settle = (ok: boolean) => {
    if (!top) return;
    top.resolve(ok);
    setStack((s) => s.slice(0, -1));
  };

  return (
    <Dialog
      open={!!top}
      onOpenChange={(o) => {
        if (!o) settle(false);
      }}
    >
      <DialogContent className="sm:max-w-md">
        {top && (
          <>
            <DialogHeader>
              <DialogTitle>{top.title || "Confirm"}</DialogTitle>
              {top.message && (
                <DialogDescription className="whitespace-pre-line">
                  {top.message}
                </DialogDescription>
              )}
            </DialogHeader>
            <DialogFooter className="gap-2 sm:gap-2">
              <Button variant="outline" onClick={() => settle(false)}>
                {top.cancelLabel || "Cancel"}
              </Button>
              <Button
                variant={top.danger ? "destructive" : "default"}
                onClick={() => settle(true)}
              >
                {top.confirmLabel || "Confirm"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
