"use client";
// App shell and route guard: picks AppBar / ExamBar / none, and redirects an
// unauthenticated visitor on a protected route to /login?next=<path+query>.
// The guard only redirects; it never reads or stores credentials.
import { useEffect } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { AppBar } from "@/components/AppBar";
import { useSession } from "@/lib/session";
import { isExamTakePath, isPublicPath, loginUrl } from "@/lib/routes";

export function Shell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const search = useSearchParams().toString();
  const router = useRouter();
  const { user, loading, error, loggedOut, refresh } = useSession();
  const isPublic = isPublicPath(pathname);

  useEffect(() => {
    if (isPublic || loading || user || error) return;
    // After Log out go to plain /login; otherwise remember where to return.
    router.replace(loggedOut ? "/login" : loginUrl(pathname + (search ? `?${search}` : "")));
  }, [isPublic, loading, user, error, loggedOut, pathname, search, router]);

  if (isPublic) return <>{children}</>;
  if (error && !user) {
    return (
      <div className="page py-5" role="alert">
        <p className="mb-3">Couldn&apos;t load your account. Try again.</p>
        <button type="button" className="btn-secondary" onClick={() => void refresh()}>Retry</button>
      </div>
    );
  }
  if (loading || !user) return <div className="page py-5 text-muted" role="status">Loading…</div>;
  // The exam take page renders its own ExamBar (fs 4).
  if (isExamTakePath(pathname)) return <>{children}</>;
  return (
    <>
      <AppBar />
      {error && (
        <div className="page pt-3" role="alert">
          <span className="mr-3 text-sm">Couldn&apos;t load your account. Try again.</span>
          <button type="button" className="btn-secondary" onClick={() => void refresh()}>Retry</button>
        </div>
      )}
      {children}
    </>
  );
}
