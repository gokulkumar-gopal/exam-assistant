import { AttemptProvider } from "@/components/AttemptProvider";

// Shared by /exams/[id]/take and /exams/[id]/review: one attempt state, one autosaver, one timer, so answers
// survive moving between the two pages and the browser Back button (a refresh reloads them from the server).
export default function Layout({ children }: { children: React.ReactNode }) {
  return <AttemptProvider>{children}</AttemptProvider>;
}
