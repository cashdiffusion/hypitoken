import { REGEXP_ONLY_DIGITS } from "input-otp";
import { motion, useReducedMotion } from "motion/react";
import { useEffect, useRef } from "react";
import { InputOTP, InputOTPGroup, InputOTPSlot, type OtpState } from "@/components/ui/input-otp";
import { celebrate } from "@/lib/confetti";

// OtpField — the 6-cell verification-code input shared by sign-up and password
// reset. Handles the full interaction: auto-focus, digit-only entry, paste,
// auto-submit on the 6th digit (onComplete), an error shake, and a confetti
// celebration anchored to the field on success.
export function OtpField({
  value,
  onChange,
  onComplete,
  state = "idle",
  disabled,
}: {
  value: string;
  onChange: (v: string) => void;
  onComplete?: (v: string) => void;
  state?: OtpState;
  disabled?: boolean;
}) {
  const reduce = useReducedMotion();
  const ref = useRef<HTMLDivElement>(null);
  const celebrated = useRef(false);

  // Fire confetti once when we enter the success state, originating from the
  // centre of the field so it reads as "these cells just lit up".
  useEffect(() => {
    if (state !== "success") {
      celebrated.current = false;
      return;
    }
    if (celebrated.current) return;
    celebrated.current = true;
    let origin = { x: 0.5, y: 0.4 };
    const el = ref.current;
    if (el) {
      const r = el.getBoundingClientRect();
      origin = {
        x: (r.left + r.width / 2) / window.innerWidth,
        y: (r.top + r.height / 2) / window.innerHeight,
      };
    }
    celebrate(origin);
  }, [state]);

  // Lock entry while verifying or after success so the cells don't change
  // under the animation.
  const locked = disabled || state === "verifying" || state === "success";

  return (
    <motion.div
      ref={ref}
      className="flex justify-center py-1"
      animate={state === "error" && !reduce ? { x: [0, -9, 9, -7, 7, -4, 4, 0] } : { x: 0 }}
      transition={{ duration: 0.45, ease: "easeInOut" }}
    >
      <InputOTP
        maxLength={6}
        value={value}
        onChange={onChange}
        onComplete={onComplete}
        pattern={REGEXP_ONLY_DIGITS}
        inputMode="numeric"
        disabled={locked}
        autoFocus
      >
        <InputOTPGroup>
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <InputOTPSlot key={i} index={i} state={state} />
          ))}
        </InputOTPGroup>
      </InputOTP>
    </motion.div>
  );
}
