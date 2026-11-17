"use client";

import { useEffect, useRef, type ReactNode } from "react";
import styles from "./profile.module.css";

/** A closed-by-default section that opens when the page's #hash points at it (the cards' ↗ links). */
export function Disclosure({ id, title, hint, children }: { id: string; title: string; hint: string; children: ReactNode }) {
  const ref = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const open = () => {
      if (location.hash === `#${id}` && ref.current) {
        ref.current.open = true;
        ref.current.scrollIntoView({ block: "start" });
      }
    };
    open();
    window.addEventListener("hashchange", open);
    return () => window.removeEventListener("hashchange", open);
  }, [id]);
  return (
    <details id={id} ref={ref} className={styles.disclosure}>
      <summary>
        <strong>{title}</strong>
        <span className="faint">{hint}</span>
        <span className={styles.chev} aria-hidden="true">›</span>
      </summary>
      <div className={styles.disclosureBody}>{children}</div>
    </details>
  );
}
