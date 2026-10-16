// "💡 N left" in the exam header (fs 7 PeeksCounter). The text is always present; the emoji is decoration.
export function PeeksCounter({ left, className }: { left: number; className?: string }) {
  return (
    <span className={className} data-testid="peeks-counter">
      <span aria-hidden="true">💡 </span>
      <span className="sr-only">Peeks </span>
      {left} left
    </span>
  );
}
