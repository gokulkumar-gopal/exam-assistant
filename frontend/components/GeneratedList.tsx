import type { GeneratedResult } from "@/lib/generated";

/** Stub for F-2.4, which replaces this body with the editable question list. */
export function GeneratedList({ result }: { result: GeneratedResult }) {
  return <p className="card p-4 font-semibold" role="status">Generated {result.questions.length} questions</p>;
}
