"use client";
// fs 8.1 errors table: 403 shows this toast and goes Home. One place so every screen does the same thing.
import { useCallback } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { ApiError } from "./api";

export const FORBIDDEN_TOAST = "You don't have access to that.";

export const isForbidden = (e: unknown): boolean => e instanceof ApiError && e.status === 403;

/** Returns a handler: for a 403 it toasts, goes Home and returns true; otherwise it does nothing and returns false. */
export function useForbiddenRedirect(): (e: unknown) => boolean {
  const router = useRouter();
  return useCallback((e: unknown) => {
    if (!isForbidden(e)) return false;
    toast(FORBIDDEN_TOAST);
    router.replace("/");
    return true;
  }, [router]);
}
