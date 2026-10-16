// Pure route helpers for the shell and guards (fs 4, 5).
const PUBLIC_PATHS = ["/login", "/signup"];

export function isPublicPath(pathname: string): boolean {
  return PUBLIC_PATHS.includes(pathname);
}

/** `/exams/<id>/take` uses ExamBar instead of AppBar (fs 4). */
export function isExamTakePath(pathname: string): boolean {
  return /^\/exams\/[^/]+\/take\/?$/.test(pathname);
}

/** Login URL carrying the path (and query) to return to. */
export function loginUrl(pathWithQuery: string): string {
  return `/login?next=${encodeURIComponent(pathWithQuery)}`;
}

/** pathname plus the raw query string (with or without its leading "?"), for the login `next` param. */
export function returnPath(pathname: string, search: string): string {
  const q = search.replace(/^\?/, "");
  return q ? `${pathname}?${q}` : pathname;
}

/** A nav link is active on its own path; Home only on `/`. */
export function isActiveLink(href: string, pathname: string): boolean {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

export const NAV_LINKS = [
  { href: "/", label: "Home" },
  { href: "/documents", label: "Documents" },
  { href: "/create", label: "Create" },
] as const;
