import type { PrintPlan } from "@/lib/print";

const LETTERS = ["A", "B", "C", "D"];

function NameLine({ total }: { total?: number }) {
  return (
    <div className="print-nameline">
      <span>Name: <span className="print-blank print-blank-lg" /></span>
      <span>Date: <span className="print-blank print-blank-md" /></span>
      {total !== undefined && <span>Score: <span className="print-blank print-blank-sm" /> / {total}</span>}
    </div>
  );
}

/**
 * The printed pages, also used as the live preview (fs 6.9). It receives a PrintPlan only, which carries key
 * material only when the key is visible. Every string is a React text node; nothing is injected as HTML.
 */
export function PrintSheet({ plan }: { plan: PrintPlan }) {
  const { paper, answerSheet, key } = plan;
  return (
    <div className="print-root">
      <section className="print-sheet" aria-label="Question paper">
        <header className="print-header">
          <h2 className="print-title">{plan.title}</h2>
          <p className="print-meta">{plan.meta}</p>
          {plan.nameLine && <NameLine total={plan.nameLine.total} />}
        </header>
        <p className="print-instruction">{plan.instruction}</p>
        <ol className="print-questions">
          {paper.questions.map((q) => (
            <li key={q.number} className="print-q">
              <div className="print-q-row">
                <span className="print-q-num">{q.number}.</span>
                <span className="print-q-text">{q.text}</span>
                <span className="print-q-marks">{q.marksLabel}</span>
              </div>
              {q.options && (
                <ul className="print-options">
                  {q.options.map((o) => (
                    <li key={o.letter}><span className="print-opt-letter">{o.letter}.</span> {o.text}</li>
                  ))}
                </ul>
              )}
              {q.lines > 0 && (
                <div className="print-lines" aria-hidden="true">
                  {Array.from({ length: q.lines }, (_, i) => <div key={i} className="print-line" />)}
                </div>
              )}
            </li>
          ))}
        </ol>
      </section>

      {answerSheet && (
        <section className="print-sheet print-newpage" aria-label="Answer sheet">
          <h2 className="print-title">Answer sheet: {plan.title}</h2>
          {plan.nameLine && <NameLine />}
          <ul
            className={`print-grid${answerSheet.twoColumns ? " print-grid-2" : ""}`}
            style={answerSheet.twoColumns ? { gridTemplateRows: `repeat(${Math.ceil(answerSheet.numbers.length / 2)}, auto)` } : undefined}
          >
            {answerSheet.numbers.map((n) => (
              <li key={n} className="print-grid-row">
                <span className="print-grid-num">{n}</span>
                {LETTERS.map((l) => (
                  <span key={l} className="print-grid-choice"><span className="print-circle" aria-hidden="true" />{l}</span>
                ))}
              </li>
            ))}
          </ul>
        </section>
      )}

      {key && (
        <section className="print-sheet print-newpage" aria-label="Answer key">
          <h2 className="print-title">{key.heading}</h2>
          <p className="print-meta print-note">{key.note}</p>
          <ol className="print-questions">
            {key.entries.map((e) => (
              <li key={e.number} className="print-q">
                {e.letter !== undefined ? (
                  <div className="print-q-row">
                    <span className="print-q-text"><b>{e.number}. {e.letter}:</b> {e.optionText}</span>
                    <span className="print-q-marks">{e.marksLabel}</span>
                  </div>
                ) : e.modelAnswer !== undefined ? (
                  <>
                    <div className="print-q-row">
                      <span className="print-q-num">{e.number}.</span>
                      <span className="print-q-text">{e.text}</span>
                      <span className="print-q-marks">{e.marksLabel}</span>
                    </div>
                    <p className="print-model"><b>Model answer:</b> {e.modelAnswer}</p>
                  </>
                ) : (
                  <div className="print-q-row">
                    <span className="print-q-text"><b>{e.number}.</b> —</span>
                    <span className="print-q-marks">{e.marksLabel}</span>
                  </div>
                )}
              </li>
            ))}
          </ol>
        </section>
      )}

      <footer className="print-footer">{plan.title}</footer>
    </div>
  );
}
