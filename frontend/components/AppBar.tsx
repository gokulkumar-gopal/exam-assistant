"use client";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { Menu } from "@base-ui/react/menu";
import { BarChart3, LogOut } from "lucide-react";
import { useSession } from "@/lib/session";
import { NAV_LINKS, isActiveLink } from "@/lib/routes";
import { cn } from "@/lib/utils";

const ITEM =
  "flex min-h-10 cursor-pointer items-center gap-2 rounded-md px-3 text-[15px] text-ink outline-none data-[highlighted]:bg-tint";

export function AppBar() {
  const pathname = usePathname();
  const router = useRouter();
  const { user, logout } = useSession();
  const initial = (user?.name.trim()[0] ?? "?").toUpperCase();

  return (
    <header className="sticky top-0 z-30 h-14 border-b border-line bg-white">
      <div className="page flex h-full items-center gap-4">
        <Link
          href="/"
          className="flex min-h-10 items-center bg-grad bg-clip-text text-[17px] font-bold text-transparent"
        >
          Exam Assistant
        </Link>
        <nav aria-label="Main" className="hidden h-full items-stretch gap-1 sm:flex">
          {NAV_LINKS.map((l) => {
            const active = isActiveLink(l.href, pathname);
            return (
              <Link
                key={l.href}
                href={l.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex min-h-10 items-center border-b-2 px-3 text-[15px]",
                  active ? "border-accent font-semibold text-ink" : "border-transparent text-muted",
                )}
              >
                {l.label}
              </Link>
            );
          })}
        </nav>
        <div className="ml-auto">
          <Menu.Root>
            <Menu.Trigger
              aria-label="Account menu"
              className="flex size-10 cursor-pointer items-center justify-center rounded-full"
            >
              <span className="flex size-8 items-center justify-center rounded-full bg-grad text-sm font-bold text-white">
                {initial}
              </span>
            </Menu.Trigger>
            <Menu.Portal>
              <Menu.Positioner align="end" sideOffset={6} className="z-50">
                <Menu.Popup className="min-w-[200px] rounded-[10px] border border-line bg-white p-1 shadow-lg outline-none">
                  <div className="px-3 py-2 text-sm font-semibold text-ink">{user?.name}</div>
                  <div className="sm:hidden">
                    {NAV_LINKS.map((l) => (
                      <Menu.LinkItem
                        key={l.href}
                        href={l.href}
                        render={<Link href={l.href} />}
                        className={cn(ITEM, isActiveLink(l.href, pathname) && "font-semibold text-accent")}
                      >
                        {l.label}
                      </Menu.LinkItem>
                    ))}
                  </div>
                  <Menu.LinkItem href="/usage" render={<Link href="/usage" />} className={ITEM}>
                    <BarChart3 size={16} aria-hidden /> AI usage
                  </Menu.LinkItem>
                  <Menu.Item
                    className={ITEM}
                    onClick={async () => {
                      await logout();
                      router.replace("/login");
                    }}
                  >
                    <LogOut size={16} aria-hidden /> Log out
                  </Menu.Item>
                </Menu.Popup>
              </Menu.Positioner>
            </Menu.Portal>
          </Menu.Root>
        </div>
      </div>
    </header>
  );
}
