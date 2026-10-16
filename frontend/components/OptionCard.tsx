"use client";
// Multiple-choice options as a real radio group (fs 6.5): role=radiogroup / radio, aria-checked,
// roving tabindex, Arrow keys move and select (wrapping). Option text is rendered as a React text
// node only, so markup in a question or option shows literally.
import { useRef } from "react";
import { optionLetter } from "@/lib/take";
import { cn } from "@/lib/utils";

export interface OptionCardProps {
  index: number;
  text: string;
  selected: boolean;
  tabbable: boolean;
  onSelect: () => void;
  onKeyDown: (e: React.KeyboardEvent<HTMLButtonElement>) => void;
  buttonRef: (el: HTMLButtonElement | null) => void;
}

export function OptionCard({ index, text, selected, tabbable, onSelect, onKeyDown, buttonRef }: OptionCardProps) {
  return (
    <button
      ref={buttonRef}
      type="button"
      role="radio"
      aria-checked={selected}
      tabIndex={tabbable ? 0 : -1}
      onClick={onSelect}
      onKeyDown={onKeyDown}
      className={cn(
        "flex min-h-11 w-full items-start gap-2 rounded-[10px] border px-3.5 py-2.5 text-left text-[15px] hover:bg-tint",
        selected
          ? "border-accent bg-tint font-semibold shadow-[inset_4px_0_0_var(--accent2)]"
          : "border-line bg-card font-normal",
      )}
    >
      <span aria-hidden="true" className="shrink-0 font-semibold">{optionLetter(index)}</span>
      <span className="min-w-0 whitespace-pre-wrap break-words">{text}</span>
    </button>
  );
}

export interface OptionGroupProps {
  options: readonly string[];
  /** Selected option text (the API stores the option text), or undefined. */
  value: string | undefined;
  labelledBy: string;
  onChange: (text: string) => void;
}

export function OptionGroup({ options, value, labelledBy, onChange }: OptionGroupProps) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  // Only the first card with this text is selected, even if two options repeat a string.
  const selected = value === undefined ? -1 : options.indexOf(value);

  const choose = (i: number) => {
    onChange(options[i]);
    refs.current[i]?.focus();
  };

  const onKeyDown = (i: number) => (e: React.KeyboardEvent<HTMLButtonElement>) => {
    const n = options.length;
    let to = -1;
    if (e.key === "ArrowDown" || e.key === "ArrowRight") to = (i + 1) % n;
    else if (e.key === "ArrowUp" || e.key === "ArrowLeft") to = (i - 1 + n) % n;
    else if (e.key === "Home") to = 0;
    else if (e.key === "End") to = n - 1;
    if (to < 0) return;
    e.preventDefault();
    choose(to);
  };

  return (
    <div role="radiogroup" aria-labelledby={labelledBy} className="flex flex-col gap-2">
      {options.map((text, i) => (
        <OptionCard
          key={i}
          index={i}
          text={text}
          selected={i === selected}
          tabbable={selected === -1 ? i === 0 : i === selected}
          onSelect={() => choose(i)}
          onKeyDown={onKeyDown(i)}
          buttonRef={(el) => { refs.current[i] = el; }}
        />
      ))}
    </div>
  );
}
