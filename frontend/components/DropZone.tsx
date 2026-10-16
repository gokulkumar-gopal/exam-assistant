"use client";

/** Desktop only (hidden under 640 px). Dropped files go to the same queue as the pickers. */
export function DropZone({ onFiles }: { onFiles: (files: File[]) => void }) {
  return (
    <div
      data-testid="dropzone"
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault();
        onFiles(Array.from(e.dataTransfer.files));
      }}
      className="mb-4 hidden min-h-10 items-center justify-center rounded-[10px] border-2 border-dashed border-[#c4a8f5] bg-tint px-4 py-5 text-center text-[13px] text-muted sm:flex"
    >
      Drag files here to upload (PDF, JPG, PNG, TXT · up to 20 MB)
    </div>
  );
}
