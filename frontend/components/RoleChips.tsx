"use client";
import { useRef } from "react";
import type { Role } from "@/lib/types";
import { cn } from "@/lib/utils";

const ROLES: { value: Role; label: string }[] = [
  { value: "student", label: "Student" },
  { value: "parent", label: "Parent" },
  { value: "teacher", label: "Teacher" },
];

interface Props {
  value: Role | null;
  onChange: (role: Role) => void;
  /** Draws attention to the group (used when the server asks for a role). */
  highlight?: boolean;
  disabled?: boolean;
  /** Id of the element that labels the group. */
  labelledBy?: string;
  describedBy?: string;
  invalid?: boolean;
}

/** Radio group of role chips; arrow keys move and select (roving tabindex). */
export function RoleChips({ value, onChange, highlight, disabled, labelledBy, describedBy, invalid }: Props) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const focusIndex = Math.max(0, ROLES.findIndex((r) => r.value === value));

  function onKeyDown(e: React.KeyboardEvent, i: number) {
    let next = i;
    if (e.key === "ArrowRight" || e.key === "ArrowDown") next = (i + 1) % ROLES.length;
    else if (e.key === "ArrowLeft" || e.key === "ArrowUp") next = (i - 1 + ROLES.length) % ROLES.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = ROLES.length - 1;
    else return;
    e.preventDefault();
    onChange(ROLES[next].value);
    refs.current[next]?.focus();
  }

  return (
    <div
      role="radiogroup"
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      aria-invalid={invalid || undefined}
      className={cn("flex flex-wrap gap-2 rounded-[10px]", highlight && "p-1.5 ring-2 ring-accent2")}
    >
      {ROLES.map((r, i) => {
        const on = value === r.value;
        return (
          <button
            key={r.value}
            ref={(el) => { refs.current[i] = el; }}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={i === focusIndex ? 0 : -1}
            disabled={disabled}
            onClick={() => onChange(r.value)}
            onKeyDown={(e) => onKeyDown(e, i)}
            className={cn(
              "min-h-10 min-w-10 cursor-pointer rounded-full border px-4 text-[15px] disabled:cursor-not-allowed disabled:opacity-40",
              on ? "border-transparent bg-grad font-semibold text-white" : "border-line bg-card hover:bg-tint",
            )}
          >
            {r.label}
          </button>
        );
      })}
    </div>
  );
}
