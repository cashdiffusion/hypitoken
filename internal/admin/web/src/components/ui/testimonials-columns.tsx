import React from "react";
import { motion } from "motion/react";
import { Quote } from "lucide-react";

export interface Testimonial {
  text: string;
  name: string;
  role: string;
}

interface TestimonialsColumnProps {
  className?: string;
  testimonials: Testimonial[];
  duration?: number;
  /** Honor reduced-motion: render a static stack instead of the marquee. */
  reduce?: boolean;
}

// Vertical marquee column of glass testimonial cards. Re-themed from the
// 21st.dev "testimonials-columns-1" block onto the project's `.glass` surface
// and design tokens. Initials avatars keep it on-brand and free of external
// image requests.
export function TestimonialsColumn({ className, testimonials, duration = 10, reduce }: TestimonialsColumnProps) {
  const cards = (
    <>
      {testimonials.map(({ text, name, role }, i) => (
        <figure key={i} className="glass w-full max-w-xs rounded-3xl p-7">
          <Quote className="h-5 w-5 text-primary/60" aria-hidden />
          <blockquote className="mt-3 text-sm leading-relaxed text-foreground/85">{text}</blockquote>
          <figcaption className="mt-6 flex items-center gap-3">
            <Avatar name={name} />
            <div className="flex flex-col">
              <span className="font-medium leading-5 tracking-tight text-foreground">{name}</span>
              <span className="text-sm leading-5 text-muted-foreground">{role}</span>
            </div>
          </figcaption>
        </figure>
      ))}
    </>
  );

  if (reduce) {
    return (
      <div className={className}>
        <div className="flex flex-col gap-6">{cards}</div>
      </div>
    );
  }

  return (
    <div className={className}>
      <motion.div
        animate={{ translateY: "-50%" }}
        transition={{ duration, repeat: Infinity, ease: "linear", repeatType: "loop" }}
        className="flex flex-col gap-6 pb-6"
      >
        {[0, 1].map((dup) => (
          <React.Fragment key={dup}>{cards}</React.Fragment>
        ))}
      </motion.div>
    </div>
  );
}

function Avatar({ name }: { name: string }) {
  const initials = name
    .trim()
    .split(/\s+/)
    .map((w) => w[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
  return (
    <div className="grid h-10 w-10 shrink-0 place-items-center rounded-full bg-primary/15 text-sm font-semibold text-primary ring-1 ring-primary/20">
      {initials}
    </div>
  );
}
