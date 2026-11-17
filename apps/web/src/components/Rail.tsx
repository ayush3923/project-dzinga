"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const icon = (d: string) => (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8"
    strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d={d} />
  </svg>
);

type Item = { href: string; label: string; icon: React.ReactNode };

const TRACK: Item[] = [
  { href: "/", label: "Today", icon: icon("M12 3v2M12 19v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M3 12h2M19 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8z") },
  { href: "/pipeline", label: "Pipeline", icon: icon("M4 5h4v14H4zM10 5h4v9h-4zM16 5h4v5h-4z") },
  { href: "/organizations", label: "Organizations", icon: icon("M3 21h18M5 21V7l7-4 7 4v14M9 9h1M14 9h1M9 13h1M14 13h1M9 17h1M14 17h1") },
  { href: "/analysis", label: "Analysis", icon: icon("M4 20V10M10 20V4M16 20v-7M22 20H2") },
  { href: "/quiz", label: "Daily quiz", icon: icon("M9 9a3 3 0 1 1 4 2.8c-.6.3-1 .9-1 1.6V14M12 17h.01M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z") },
];

const CONFIGURE: Item[] = [
  { href: "/intent", label: "Intent", icon: icon("M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM12 12h.01") },
  { href: "/profile", label: "Profile", icon: icon("M20 21a8 8 0 0 0-16 0M12 13a5 5 0 1 0 0-10 5 5 0 0 0 0 10z") },
];

// Later PRD stages, shown collapsed so the nav stays simple (progressive disclosure).
const SOON = ["Discover", "Prepare", "Hiring news"];

export function Rail() {
  const path = usePathname();
  return (
    <aside className="rail">
      <Link href="/" className="brand" aria-label="Dzinga home">
        <div className="brand-mark" aria-hidden="true" />
        <span>Dzinga</span>
      </Link>
      <nav className="nav" aria-label="Main">
        <div className="nav-section">Track your search</div>
        {TRACK.map((n) => <NavLink key={n.href} item={n} path={path} />)}
        <div className="nav-section">Configure</div>
        {CONFIGURE.map((n) => <NavLink key={n.href} item={n} path={path} />)}
        <details>
          <summary>
            {icon("M12 5v14M5 12h14")}
            More
          </summary>
          {SOON.map((s) => (
            <div key={s} className="soon">
              <span>{s}</span>
              <span>soon</span>
            </div>
          ))}
        </details>
      </nav>
      <div className="rail-foot">Local data only</div>
    </aside>
  );
}

function NavLink({ item, path }: { item: Item; path: string }) {
  const active = item.href === "/" ? path === "/" : path.startsWith(item.href);
  return (
    <Link href={item.href} aria-current={active ? "page" : undefined}>
      {item.icon}
      {item.label}
    </Link>
  );
}
