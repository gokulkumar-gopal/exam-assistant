import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Exam Assistant",
  description: "Create, take and review exams from your documents.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
