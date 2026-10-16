export const MARK_COLORS = ["#0d9488", "#2563eb", "#7c3aed", "#ea580c", "#e11d48"] as const;

/** Colour for a mark value 1..5 (fs 3). Out-of-range values are clamped. */
export function markColor(marks: number): string {
  const i = Math.min(5, Math.max(1, Math.round(marks))) - 1;
  return MARK_COLORS[i];
}
