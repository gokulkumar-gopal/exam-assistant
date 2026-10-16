import { cn } from "@/lib/utils";

export type MeterTone = "accent" | "ok" | "warn" | "bad";

const TONES: Record<MeterTone, string> = {
  accent: "bg-grad",
  ok: "bg-ok",
  warn: "bg-warn",
  bad: "bg-bad",
};

export function Meter({
  value,
  max,
  tone = "accent",
  label,
}: {
  value: number;
  max: number;
  tone?: MeterTone;
  label?: string;
}) {
  const pct = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0;
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={Math.min(max, Math.max(0, value))}
      data-tone={tone}
      className="h-2 w-full overflow-hidden rounded-[99px] bg-line"
    >
      <i
        className={cn(
          "block h-full transition-[width] duration-200 motion-reduce:transition-none",
          TONES[tone],
        )}
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}
