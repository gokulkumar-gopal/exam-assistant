import type { Metadata } from "next";
import { Suspense } from "react";
import "./globals.css";
import { SessionProvider } from "@/lib/session";
import { Toaster } from "@/components/ui/sonner";
import { Shell } from "@/components/Shell";

export const metadata: Metadata = {
  title: "Exam Assistant",
  description: "Create, take and review exams from your documents.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <SessionProvider>
          <Suspense fallback={null}>
            <Shell>{children}</Shell>
          </Suspense>
        </SessionProvider>
        <Toaster />
      </body>
    </html>
  );
}
