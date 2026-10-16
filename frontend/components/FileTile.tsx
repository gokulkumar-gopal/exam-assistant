import { cn } from "@/lib/utils";

export type FileType = "pdf" | "jpg" | "png" | "txt";

const COLOURS: Record<FileType, string> = {
  pdf: "bg-file-pdf",
  jpg: "bg-file-jpg",
  png: "bg-file-png",
  txt: "bg-file-txt",
};

export function FileTile({ type }: { type: FileType }) {
  return (
    <span
      data-type={type}
      className={cn(
        "inline-flex h-11 w-9 shrink-0 items-center justify-center rounded-[6px] text-[10px] font-bold text-white",
        COLOURS[type],
      )}
    >
      {type.toUpperCase()}
    </span>
  );
}
