"use client";
// App shell and route guard: picks AppBar / ExamBar / none, and redirects an
// unauthenticated visitor on a protected route to /login?next=<path+query>.
// The guard only redirects; it never reads or stores credentials.
import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";
import { ReachabilityNotice } from "@/components/ReachabilityNotice";
import { AppBar } from "@/components/AppBar";
import { useSession } from "@/lib/session";
import { isExamTakePath, isPublicPath, loginUrl, returnPath } from "@/lib/routes";

export function Shell({ children }: { children: React.ReactNode }) {
  return (
    <>
      <ReachabilityNotice />
      <ShellInner>{children}</ShellInner>
    </>
  );
}

function ShellInner({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const { user, loading, error, loggedOut, refresh } = useSession();
  const isPublic = isPublicPath(pathname);

  useEffect(() => {
    if (isPublic || loading || user || error) return;
    // After Log out go to plain /login; otherwise remember where to return.
    // The query is read here, in the effect, rather than via useSearchParams: that hook forces a
    // Suspense boundary around the whole shell, and a late-hydrating boundary can see the session
    // already resolved (user set) while the server HTML said "Loading…", which is React error #418.
    router.replace(loggedOut ? "/login" : loginUrl(returnPath(pathname, window.location.search)));
  }, [isPublic, loading, user, error, loggedOut, pathname, router]);

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
        <div className="page pt-3 print:hidden" role="alert">
          <span className="mr-3 text-sm">Couldn&apos;t load your account. Try again.</span>
          <button type="button" className="btn-secondary" onClick={() => void refresh()}>Retry</button>
        </div>
      )}
      {children}
    </>
  );
}
