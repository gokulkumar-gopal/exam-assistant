import { describe, expect, it } from "vitest";
import {
  ANSWER_LABEL, applyReveal, applyRevealError, initReveal, peeksOn, peeksLeftText, revealControl,
  revealDialogBody, revealedAnswerText, storedAnswer,
} from "../lib/reveal";

const fresh = () => initReveal(3, { revealed: [], peeksLeft: 3 });
const resp = (questionId: string, extra: object = {}, peeksLeft = 2) => ({ questionId, peeksLeft, serverTime: "t", ...extra });

describe("labels", () => {
  it("uses the spec text", () => {
    expect(ANSWER_LABEL).toBe("Revealed · half marks at most");
    expect(revealDialogBody(2)).toBe("Revealed questions score at most half marks. You have 2 peeks left.");
    expect(revealDialogBody(1)).toBe("Revealed questions score at most half marks. You have 1 peek left.");
    expect(peeksLeftText(0)).toBe("0 peeks");
  });
});

describe("initReveal", () => {
  it("reads revealed answers and peeksLeft from the attempt", () => {
    const s = initReveal(3, { revealed: [{ questionId: "q2", correctIndex: 1 }, { questionId: "q4", modelAnswer: "m" }], peeksLeft: 1 });
    expect(s.peeksLeft).toBe(1);
    expect(storedAnswer(s, "q2")).toEqual({ correctIndex: 1 });
    expect(storedAnswer(s, "q4")).toEqual({ modelAnswer: "m" });
    expect(storedAnswer(s, "q1")).toBeUndefined();
  });
  it("derives peeksLeft when the server did not send it, never negative, never above max", () => {
    expect(initReveal(3, { revealed: [{ questionId: "a", correctIndex: 0 }] }).peeksLeft).toBe(2);
    expect(initReveal(1, { revealed: [{ questionId: "a" }, { questionId: "b" }] }).peeksLeft).toBe(0);
    expect(initReveal(3, { peeksLeft: -4 }).peeksLeft).toBe(0);
    expect(initReveal(3, { peeksLeft: 9 }).peeksLeft).toBe(3);
    expect(initReveal(3, null).peeksLeft).toBe(3);
    expect(initReveal(3, { peeksLeft: Number.NaN }).peeksLeft).toBe(3);
  });
  it("keeps only the answer fields and ignores junk entries", () => {
    const s = initReveal(3, { revealed: [{ questionId: "a", correctIndex: 2, extra: "x" } as never, { questionId: 7 } as never, null as never] });
    expect(storedAnswer(s, "a")).toEqual({ correctIndex: 2 });
    expect(Object.keys(s.revealed)).toEqual(["a"]);
  });
  it("is off when the key is already visible", () => {
    expect(peeksOn(initReveal(3, null, true), true)).toBe(false);
  });
});

describe("peeksOn (maxPeeks <= 0 hides everything)", () => {
  it("is on only for maxPeeks > 0 with the UI flag on", () => {
    expect(peeksOn(fresh(), true)).toBe(true);
    expect(peeksOn(fresh(), false)).toBe(false);
    expect(peeksOn(initReveal(0, null), true)).toBe(false);
    expect(peeksOn(initReveal(-1, null), true)).toBe(false);
    expect(revealControl(initReveal(0, null), "q1", false, true)).toBe("none");
  });
});

describe("revealControl", () => {
  it("is enabled for an unrevealed question", () => {
    expect(revealControl(fresh(), "q1", false, true)).toBe("enabled");
  });
  it("is gone for a revealed question (its answer is on screen)", () => {
    const s = applyReveal(fresh(), resp("q1", { correctIndex: 0 }));
    expect(revealControl(s, "q1", false, true)).toBe("none");
    expect(revealControl(s, "q2", false, true)).toBe("enabled");
  });
  it("is disabled while a submit or another reveal is running", () => {
    expect(revealControl(fresh(), "q1", true, true)).toBe("disabled");
  });
  it("is disabled with no peeks left, and gone when the UI flag is off", () => {
    expect(revealControl(initReveal(3, { peeksLeft: 0 }), "q1", false, true)).toBe("disabled");
    expect(revealControl(fresh(), "q1", false, false)).toBe("none");
  });
});

describe("applyReveal", () => {
  it("stores the answer and takes peeksLeft from the server", () => {
    const s = applyReveal(fresh(), resp("q1", { correctIndex: 2 }, 2));
    expect(s.peeksLeft).toBe(2);
    expect(storedAnswer(s, "q1")).toEqual({ correctIndex: 2 });
  });
  it("does not mutate its input", () => {
    const a = fresh();
    applyReveal(a, resp("q1", { correctIndex: 2 }));
    expect(a.revealed).toEqual({});
    expect(a.peeksLeft).toBe(3);
  });
  it("revealing the same question again costs nothing and keeps the stored answer", () => {
    const once = applyReveal(fresh(), resp("q1", { correctIndex: 2 }, 2));
    const twice = applyReveal(once, resp("q1", { correctIndex: 3 }, 1));
    expect(twice).toBe(once);
    expect(twice.peeksLeft).toBe(2);
    expect(storedAnswer(twice, "q1")).toEqual({ correctIndex: 2 });
  });
  it("never lets peeksLeft go negative or above max", () => {
    expect(applyReveal(fresh(), resp("q1", { correctIndex: 0 }, -3)).peeksLeft).toBe(0);
    expect(applyReveal(fresh(), resp("q1", { correctIndex: 0 }, 99)).peeksLeft).toBe(3);
  });
  it("a free-text answer is kept as text", () => {
    const s = applyReveal(fresh(), resp("q1", { modelAnswer: "<b>m</b>" }));
    expect(storedAnswer(s, "q1")).toEqual({ modelAnswer: "<b>m</b>" });
  });
});

describe("applyRevealError", () => {
  it("409 (no peeks left / key visible) disables the button but keeps what is revealed", () => {
    const s0 = applyReveal(fresh(), resp("q1", { correctIndex: 0 }));
    const s = applyRevealError(s0, 409);
    expect(revealControl(s, "q2", false, true)).toBe("disabled");
    expect(storedAnswer(s, "q1")).toEqual({ correctIndex: 0 });
    expect(peeksOn(s, true)).toBe(true);
  });
  it("403 (peeking is off) removes the control and the counter", () => {
    const s = applyRevealError(fresh(), 403);
    expect(revealControl(s, "q1", false, true)).toBe("none");
    expect(peeksOn(s, true)).toBe(false);
  });
  it("other statuses change nothing", () => {
    const s = fresh();
    expect(applyRevealError(s, 500)).toBe(s);
    expect(applyRevealError(s, 0)).toBe(s);
    expect(applyRevealError(s, 404)).toBe(s);
  });
});

describe("revealedAnswerText", () => {
  const opts = ["Alpha", "Beta", "Gamma", "Delta"];
  it("MCQ: the option text at correctIndex", () => {
    expect(revealedAnswerText("mcq", opts, { correctIndex: 1 })).toBe("Beta");
    expect(revealedAnswerText("mcq", opts, { correctIndex: 0 })).toBe("Alpha");
  });
  it("MCQ: an out-of-range or missing index gives null, never undefined text", () => {
    expect(revealedAnswerText("mcq", opts, { correctIndex: 9 })).toBeNull();
    expect(revealedAnswerText("mcq", opts, { correctIndex: -1 })).toBeNull();
    expect(revealedAnswerText("mcq", opts, {})).toBeNull();
    expect(revealedAnswerText("mcq", undefined, { correctIndex: 1 })).toBeNull();
  });
  it("free text: the model answer, literally", () => {
    expect(revealedAnswerText("free", undefined, { modelAnswer: "<i>x</i>" })).toBe("<i>x</i>");
    expect(revealedAnswerText("free", undefined, { modelAnswer: "" })).toBeNull();
    expect(revealedAnswerText("free", undefined, { correctIndex: 1 })).toBeNull();
  });
});
