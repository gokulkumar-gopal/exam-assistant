import { describe, expect, it } from "vitest";
import { keyAvailableLine, peekLines, resultCardData, resultCardView, scoreFraction, scoreSubtitle, selfCheckScore, setCheck } from "../lib/key";
import { scoreText } from "../lib/format";

const leaky = {
  id: "q1", marks: 2, text: "Pick one", options: ["A1", "B1"], yourAnswer: "A1",
  correctIndex: 1, modelAnswer: "The model answer", correct: false, correctAnswer: "B1", revealed: false,
};

describe("resultCardView", () => {
  it("hides the pill and the correct line when the key is hidden, even if the object carries the key", () => {
    expect(resultCardView(leaky, false, "mcq")).toEqual({ showPill: false, showCorrect: false });
    expect(resultCardView({ correctIndex: 1, modelAnswer: "x" }, false, "mcq")).toEqual({ showPill: false, showCorrect: false });
  });
  it("shows the pill and the correct line when the key is visible", () => {
    expect(resultCardView(leaky, true, "mcq")).toEqual({ showPill: true, showCorrect: true });
    expect(resultCardView({ correctIndex: 1, modelAnswer: "x" }, true, "mcq")).toEqual({ showPill: true, showCorrect: true });
  });
  it("free text gets no pill, only the model answer", () => {
    expect(resultCardView({ modelAnswer: "m", correct: undefined }, true, "free")).toEqual({ showPill: false, showCorrect: true });
  });
  it("a visible key with nothing to show shows nothing", () => {
    expect(resultCardView({}, true, "mcq")).toEqual({ showPill: false, showCorrect: false });
  });
});

describe("resultCardData", () => {
  it("never carries key material when hidden", () => {
    const d = resultCardData(leaky, false, 1, "mcq");
    expect(JSON.stringify(d)).not.toContain("B1");
    expect(JSON.stringify(d)).not.toContain("The model answer");
    expect(d.pill).toBeNull();
    expect(d.correctText).toBeUndefined();
    expect(d.modelAnswer).toBeUndefined();
    expect(d.canSelfCheck).toBe(false);
    expect(d.yourAnswer).toBe("A1");
  });
  it("MCQ visible: pill with a text label and the correct text", () => {
    expect(resultCardData(leaky, true, 1, "mcq").pill).toEqual({ variant: "bad", text: "✗ Incorrect" });
    expect(resultCardData({ ...leaky, correct: true }, true, 1, "mcq").pill).toEqual({ variant: "ok", text: "✓ Correct" });
    expect(resultCardData(leaky, true, 1, "mcq").correctText).toBe("B1");
  });
  it("falls back to options[correctIndex] when correctAnswer is absent", () => {
    expect(resultCardData({ options: ["x", "y"], correctIndex: 1, correct: true }, true, 1, "mcq").correctText).toBe("y");
  });
  it("free text visible: model answer and self-check; markup stays literal", () => {
    const d = resultCardData({ id: "f", marks: 3, text: "<b>x</b>", yourAnswer: "mine", modelAnswer: "<i>m</i>" }, true, 2, "free");
    expect(d.kind).toBe("free");
    expect(d.canSelfCheck).toBe(true);
    expect(d.modelAnswer).toBe("<i>m</i>");
    expect(d.text).toBe("<b>x</b>");
    expect(d.number).toBe(2);
  });
  it("a blank answer reads as no answer", () => {
    expect(resultCardData({ yourAnswer: "" }, false, 1, "mcq").yourAnswer).toBe("No answer");
    expect(resultCardData({}, false, 1, "mcq").yourAnswer).toBe("No answer");
    expect(resultCardData({ yourAnswer: "  \n " }, false, 1, "mcq").yourAnswer).toBe("No answer");
  });
});

describe("card kind comes from the exam type, never from the payload shape", () => {
  // A Go handler may emit these zero values for free-text questions.
  const goFree = { id: "f", marks: 3, text: "Explain", options: [] as string[], correct: false, correctIndex: 0, yourAnswer: "mine", modelAnswer: "The model" };
  it("a free exam renders free text even with options [], correct false, correctIndex 0", () => {
    expect(resultCardView(goFree, true, "free")).toEqual({ showPill: false, showCorrect: true });
    const d = resultCardData(goFree, true, 1, "free");
    expect(d.kind).toBe("free");
    expect(d.pill).toBeNull();
    expect(d.modelAnswer).toBe("The model");
    expect(d.correctText).toBeUndefined();
    expect(d.canSelfCheck).toBe(true);
  });
  it("a free exam with the key hidden still leaks nothing", () => {
    expect(resultCardView(goFree, false, "free")).toEqual({ showPill: false, showCorrect: false });
    const d = resultCardData(goFree, false, 1, "free");
    expect(JSON.stringify(d)).not.toContain("The model");
    expect(d.canSelfCheck).toBe(false);
  });
  it("an MCQ exam is unaffected, and a question with no options or flags is still MCQ", () => {
    expect(resultCardData({ ...leaky, correct: true }, true, 1, "mcq").pill?.text).toBe("✓ Correct");
    expect(resultCardData({ correct: false, correctAnswer: "y" }, true, 1, "mcq").kind).toBe("mcq");
  });
});

describe("score text", () => {
  it("shows halves and drops .0", () => {
    expect(scoreFraction(27.5, 40)).toBe("27.5 / 40");
    expect(scoreFraction(34, 40)).toBe("34 / 40");
    expect(scoreFraction(34.0, 40.0)).toBe("34 / 40");
    expect(scoreSubtitle(34, 40)).toBe("Score 34 / 40");
    expect(scoreSubtitle(27.5, 40)).toBe("Score 27.5 / 40");
    expect(scoreText(27.5)).toBe("27.5");
  });
});

describe("keyAvailableLine", () => {
  it("formats the time with the existing date helper", () => {
    const line = keyAvailableLine("2026-10-18T18:01:00Z");
    expect(line).toMatch(/^Answers available after [A-Z][a-z]{2} \d{1,2}, \d{1,2}:\d{2} [AP]M\.$/);
  });
  it("never leaves an empty gap for a missing or unusable time", () => {
    for (const v of [null, undefined, "", "nope"]) {
      const line = keyAvailableLine(v);
      expect(line).not.toMatch(/after\s*\./);
      expect(line).not.toContain("—");
      expect(line).toBe("Answers available after the exam closes.");
    }
  });
});

describe("self-check scoring (local only)", () => {
  const qs = [{ id: "a", marks: 3, revealed: false }, { id: "b", marks: 3, revealed: true }, { id: "c", marks: 4, revealed: false }];
  it("has no score until something is checked and nothing is graded", () => {
    expect(selfCheckScore({ base: null, maxScore: 10, questions: qs, checks: {} })).toBeNull();
  });
  it("adds full marks for 'got it' and half for a revealed one", () => {
    expect(selfCheckScore({ base: null, maxScore: 10, questions: qs, checks: { a: "got" } })).toBe(3);
    expect(selfCheckScore({ base: null, maxScore: 10, questions: qs, checks: { b: "got" } })).toBe(1.5);
    expect(selfCheckScore({ base: null, maxScore: 10, questions: qs, checks: { a: "got", b: "got", c: "got" } })).toBe(8.5);
  });
  it("'missed' earns nothing", () => {
    expect(selfCheckScore({ base: null, maxScore: 10, questions: qs, checks: { a: "missed" } })).toBe(0);
  });
  it("toggling back and forth always lands on the latest choice", () => {
    let c = {};
    const s = () => selfCheckScore({ base: null, maxScore: 10, questions: qs, checks: c });
    c = setCheck(c, "a", "got"); expect(s()).toBe(3);
    c = setCheck(c, "a", "missed"); expect(s()).toBe(0);
    c = setCheck(c, "a", "got"); expect(s()).toBe(3);
    c = setCheck(c, "a", "got"); expect(s()).toBe(3);
  });
  it("setCheck does not mutate", () => {
    const c0 = { a: "got" as const };
    const c1 = setCheck(c0, "a", "missed");
    expect(c0.a).toBe("got");
    expect(c1.a).toBe("missed");
  });
  it("builds on the server score and is capped at the maximum", () => {
    expect(selfCheckScore({ base: 5, maxScore: 10, questions: qs, checks: { a: "got" } })).toBe(8);
    expect(selfCheckScore({ base: 9, maxScore: 10, questions: qs, checks: { a: "got", c: "got" } })).toBe(10);
  });
  it("ignores checks for unknown questions", () => {
    expect(selfCheckScore({ base: 4, maxScore: 10, questions: qs, checks: { zzz: "got" } })).toBe(4);
  });
});

describe("peekLines (fs 6.7)", () => {
  const base = { maxPeeks: 3, peeksUsed: 2, maxScore: 40, scoreBeforePeeks: 36, submitted: true };
  it("shows both lines when peeks were used", () => {
    expect(peekLines(base, true)).toEqual({ withoutPenalty: "Score without the peek penalty: 36 / 40", used: "Peeks used: 2 of 3", noPeeks: false });
  });
  it("halves read with one decimal only when needed", () => {
    expect(peekLines({ ...base, scoreBeforePeeks: 35.5 }, true).withoutPenalty).toBe("Score without the peek penalty: 35.5 / 40");
  });
  it("no peek lines and an ok badge when peeks were allowed but none used", () => {
    expect(peekLines({ ...base, peeksUsed: 0 }, true)).toEqual({ withoutPenalty: null, used: null, noPeeks: true });
  });
  it("shows nothing when peeking was off (maxPeeks 0)", () => {
    expect(peekLines({ ...base, maxPeeks: 0, peeksUsed: 0 }, true)).toEqual({ withoutPenalty: null, used: null, noPeeks: false });
  });
  it("used lines need no penalty-free score: a missing score drops only that line", () => {
    expect(peekLines({ ...base, scoreBeforePeeks: null }, true)).toEqual({ withoutPenalty: null, used: "Peeks used: 2 of 3", noPeeks: false });
  });
  it("shows nothing for an attempt that was never submitted, or when the peeks UI is cut", () => {
    const none = { withoutPenalty: null, used: null, noPeeks: false };
    expect(peekLines({ ...base, submitted: false }, true)).toEqual(none);
    expect(peekLines({ ...base, peeksUsed: 0, submitted: false }, true)).toEqual(none);
    expect(peekLines(base, false)).toEqual(none);
    expect(peekLines({ ...base, peeksUsed: 0 }, false)).toEqual(none);
  });
});

describe("revealed questions on the result page", () => {
  const mcqKey = { id: "q1", marks: 2, text: "Pick", options: ["A1", "B1"], yourAnswer: "A1", correctIndex: 1, correctAnswer: "B1", correct: false };
  it("hidden key: a revealed question shows the marker and its revealed answer, still without a pill", () => {
    const d = resultCardData({ ...mcqKey, revealed: true }, false, 1, "mcq", true);
    expect(d.revealed).toBe(true);
    expect(d.revealedText).toBe("B1");
    expect(d.pill).toBeNull();
    expect(d.correctText).toBeUndefined();
    expect(d.canSelfCheck).toBe(false);
  });
  it("hidden key: the revealed MCQ answer falls back to options[correctIndex]", () => {
    expect(resultCardData({ ...mcqKey, correctAnswer: undefined, revealed: true }, false, 1, "mcq", true).revealedText).toBe("B1");
  });
  it("hidden key: a revealed free-text question shows its model answer as text, with no self-check", () => {
    const d = resultCardData({ id: "f", marks: 3, text: "Explain", yourAnswer: "mine", modelAnswer: "<i>m</i>", revealed: true }, false, 2, "free", true);
    expect(d.revealed).toBe(true);
    expect(d.revealedText).toBe("<i>m</i>");
    expect(d.modelAnswer).toBeUndefined();
    expect(d.canSelfCheck).toBe(false);
  });
  it("hidden key: an unrevealed question never carries key material, even if the payload does", () => {
    const d = resultCardData({ ...mcqKey, modelAnswer: "The model", revealed: false }, false, 1, "mcq", true);
    expect(d.revealed).toBe(false);
    expect(d.revealedText).toBeUndefined();
    expect(JSON.stringify(d)).not.toContain("B1");
    expect(JSON.stringify(d)).not.toContain("The model");
  });
  it("only a real true counts as revealed", () => {
    for (const v of [undefined, null, 0, 1, "true", "yes", {}] as unknown[]) {
      const d = resultCardData({ ...mcqKey, revealed: v as boolean }, false, 1, "mcq", true);
      expect(d.revealed).toBe(false);
      expect(JSON.stringify(d)).not.toContain("B1");
    }
  });
  it("hidden key and revealed, but the server sent no answer: marker only", () => {
    const d = resultCardData({ id: "q", yourAnswer: "x", revealed: true }, false, 1, "mcq", true);
    expect(d.revealed).toBe(true);
    expect(d.revealedText).toBeUndefined();
  });
  it("visible key: the marker is added, the normal pill and correct answer stay, no separate revealed line", () => {
    const d = resultCardData({ ...mcqKey, revealed: true }, true, 1, "mcq", true);
    expect(d.revealed).toBe(true);
    expect(d.revealedText).toBeUndefined();
    expect(d.pill).toEqual({ variant: "bad", text: "✗ Incorrect" });
    expect(d.correctText).toBe("B1");
  });
  it("a hidden-key payload carrying keys for all questions renders keys only for revealed ones", () => {
    const payload = Array.from({ length: 6 }, (_, i) => ({
      id: `q${i}`, marks: 1, text: `T${i}`, options: ["x", "y"], yourAnswer: "x",
      correctIndex: 1, correctAnswer: `KEY${i}`, modelAnswer: `MODEL${i}`, correct: i % 2 === 0, revealed: i === 1 || i === 4,
    }));
    for (const type of ["mcq", "free"] as const) {
      const cards = payload.map((q, i) => resultCardData(q, false, i + 1, type, true));
      cards.forEach((c, i) => {
        const json = JSON.stringify(c);
        const shown = i === 1 || i === 4;
        for (let k = 0; k < 6; k++) {
          const leaks = json.includes(`KEY${k}`) || json.includes(`MODEL${k}`);
          expect(leaks).toBe(shown && k === i);
        }
        expect(c.pill).toBeNull();
        expect(c.correctText).toBeUndefined();
        expect(c.modelAnswer).toBeUndefined();
        expect(c.canSelfCheck).toBe(false);
      });
    }
  });
  it("resultCardView is unchanged: hidden is always no pill and no correct line", () => {
    expect(resultCardView({ ...mcqKey, revealed: true }, false, "mcq")).toEqual({ showPill: false, showCorrect: false });
  });
  it("with the peeks UI cut, nothing about reveals shows", () => {
    const d = resultCardData({ ...mcqKey, revealed: true }, false, 1, "mcq", false);
    expect(d.revealed).toBe(false);
    expect(d.revealedText).toBeUndefined();
    expect(JSON.stringify(d)).not.toContain("B1");
  });
});
