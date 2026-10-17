"use client";
// Two-handle slider over Base UI. Handles show their value in a bubble while hovered, focused or dragged.
import { Slider as SliderPrimitive } from "@base-ui/react/slider";
import { cn } from "@/lib/utils";

export function RangeSlider({
  value,
  min,
  max,
  onChange,
  labels,
  disabled,
  invalid,
  className,
}: {
  value: [number, number];
  min: number;
  max: number;
  onChange: (value: [number, number]) => void;
  /** Accessible names for the first and last handle. */
  labels: [string, string];
  disabled?: boolean;
  invalid?: boolean;
  className?: string;
}) {
  return (
    <SliderPrimitive.Root
      value={value}
      min={min}
      max={max}
      step={1}
      disabled={disabled}
      thumbCollisionBehavior="none"
      onValueChange={(v) => {
        if (Array.isArray(v) && v.length === 2) onChange([v[0], v[1]]);
      }}
      className={cn("w-full touch-none select-none", className)}
    >
      <SliderPrimitive.Control className="flex h-9 w-full items-center px-2.5">
        <SliderPrimitive.Track className="relative h-2.5 w-full rounded-full border border-line bg-tint">
          <SliderPrimitive.Indicator
            className={cn(
              "rounded-full bg-gradient-to-r from-accent to-accent2",
              invalid && "from-bad-border to-bad",
            )}
          />
          {[0, 1].map((i) => (
            <SliderPrimitive.Thumb
              key={i}
              index={i}
              getAriaLabel={() => labels[i]}
              className={cn(
                "group size-5 rounded-full border-2 border-accent bg-white shadow-md outline-none transition-transform",
                "hover:scale-110 focus-visible:ring-3 focus-visible:ring-accent/40 data-[dragging]:scale-125",
                "data-[disabled]:opacity-50",
                invalid && "border-bad",
              )}
            >
              <span
                aria-hidden="true"
                className={cn(
                  "pointer-events-none absolute -top-7 left-1/2 -translate-x-1/2 rounded-md bg-accent px-1.5 py-0.5",
                  "text-[11px] font-semibold leading-none text-white opacity-0 transition-opacity",
                  "group-hover:opacity-100 group-focus-within:opacity-100 group-data-[dragging]:opacity-100",
                )}
              >
                {value[i]}
              </span>
            </SliderPrimitive.Thumb>
          ))}
        </SliderPrimitive.Track>
      </SliderPrimitive.Control>
    </SliderPrimitive.Root>
  );
}
