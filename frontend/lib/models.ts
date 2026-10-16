// Pure helpers for the Model select (fs 6.4 card 5). The API returns models cheapest first.
import type { AiModel } from "./types";

export const COSTLIER_NOTICE =
  "This model costs more per set and uses more of the shared monthly AI budget.";

/** "{name} · {tier} · about $X per set" using the small-case cost. */
export function modelOptionLabel(m: AiModel): string {
  return `${m.name} · ${m.tier} · about $${m.approxJobCost.small.toFixed(2)} per set`;
}

/** The id of the entry marked `default`, or undefined when there is none. */
export function defaultModelId(models: AiModel[]): string | undefined {
  return models.find((m) => m.default)?.id;
}

/** True when the id is a known model that is not the cheapest (the first entry). */
export function isCostlier(models: AiModel[], id: string | undefined): boolean {
  if (id === undefined) return false;
  const i = models.findIndex((m) => m.id === id);
  return i > 0;
}
