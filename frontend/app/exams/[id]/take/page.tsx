"use client";
import { useRouter } from "next/navigation";
import { ExamBar } from "@/components/ExamBar";

export default function Page() {
  const router = useRouter();
  return (
    <>
      <ExamBar
        title="Exam"
        savedAt={null}
        deadlineIso="1970-01-01T00:00:00Z"
        onSaveExit={() => router.push("/")}
      />
      <main className="page py-5">
        <h1>Take exam</h1>
      </main>
    </>
  );
}
