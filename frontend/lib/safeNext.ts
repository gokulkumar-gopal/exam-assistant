// The `next` query param is user-controlled. Only a same-origin path is
// allowed as a post-login destination; anything else goes Home.
const MAX_LEN = 2000;

function hasControlChar(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x20 || c === 0x7f) return true;
  }
  return false;
}

function isSafePath(s: string): boolean {
  return s.startsWith("/") && !s.startsWith("//") && !s.includes("\\") && !hasControlChar(s);
}

/** Returns `next` when it is a same-origin path, otherwise "/". Checks the raw and the decoded form. */
export function safeNext(next: string | null | undefined): string {
  if (!next || next.length > MAX_LEN) return "/";
  let decoded: string;
  try {
    decoded = decodeURIComponent(next);
  } catch {
    return "/";
  }
  return isSafePath(next) && isSafePath(decoded) ? next : "/";
}
