import { OTPInput, OTPInputContext } from "input-otp";
import { motion } from "motion/react";
import * as React from "react";
import { cn } from "@/lib/utils";

const EASE = [0.22, 1, 0.36, 1] as const;

// Visual state for the whole OTP field, driven by the verification request.
export type OtpState = "idle" | "verifying" | "error" | "success";

function InputOTP({
  className,
  containerClassName,
  ...props
}: React.ComponentProps<typeof OTPInput>) {
  return (
    <OTPInput
      data-slot="input-otp"
      containerClassName={cn(
        "flex items-center gap-2 has-[:disabled]:opacity-60",
        containerClassName,
      )}
      className={cn("disabled:cursor-not-allowed", className)}
      {...props}
    />
  );
}

function InputOTPGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="input-otp-group"
      className={cn("flex items-center gap-2 sm:gap-2.5", className)}
      {...props}
    />
  );
}

// A single digit cell. Independent rounded boxes with a gap (modern OTP look),
// emerald accent on the active cell, success/error tints driven by `state`.
function InputOTPSlot({
  index,
  state = "idle",
  className,
  ...props
}: React.ComponentProps<"div"> & { index: number; state?: OtpState }) {
  const ctx = React.useContext(OTPInputContext);
  const slot = ctx?.slots[index];
  const char = slot?.char;
  const isActive = slot?.isActive;

  return (
    <div
      data-slot="input-otp-slot"
      data-active={isActive}
      data-state={state}
      className={cn(
        "relative grid h-12 w-11 place-items-center rounded-xl border-2 border-input bg-background/60 font-mono text-xl font-semibold tabular-nums shadow-sm backdrop-blur-sm transition-[color,border-color,background-color,box-shadow,transform] duration-200 sm:h-14 sm:w-12",
        "data-[active=true]:z-10 data-[active=true]:scale-[1.05] data-[active=true]:border-primary data-[active=true]:ring-4 data-[active=true]:ring-primary/20",
        "data-[state=success]:border-emerald-500 data-[state=success]:bg-emerald-500/10 data-[state=success]:text-emerald-600 dark:data-[state=success]:text-emerald-400",
        "data-[state=error]:border-destructive data-[state=error]:bg-destructive/5 data-[state=error]:text-destructive",
        className,
      )}
      {...props}
    >
      {char && (
        <motion.span
          key={char}
          initial={{ opacity: 0, y: -6, scale: 0.55 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          transition={{ duration: 0.18, ease: EASE }}
        >
          {char}
        </motion.span>
      )}
    </div>
  );
}

export { InputOTP, InputOTPGroup, InputOTPSlot };
