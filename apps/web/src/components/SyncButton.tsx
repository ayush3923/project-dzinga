"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState, useTransition } from "react";
import { refreshAfterSyncAction, startSyncAction, syncStatusAction, type SyncSummary } from "@/app/actions";
import styles from "./SyncButton.module.css";

function ago(iso: string | null): string {
  if (!iso) return "never";
  const mins = Math.floor((Date.now() - Date.parse(iso)) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const h = Math.floor(mins / 60);
  if (h < 24) return `${h} h ago`;
  return `${Math.floor(h / 24)} d ago`;
}

/** Sync now, on every page: scans the selected mailboxes and refreshes the app. */
export function SyncButton() {
  const router = useRouter();
  const [s, setS] = useState<SyncSummary | null>(null);
  const [error, setError] = useState<string>();
  const [pending, start] = useTransition();
  const wasRunning = useRef(false);

  const poll = useCallback(async () => {
    const next = await syncStatusAction();
    setS(next);
    const running = next.run?.running ?? false;
    if (wasRunning.current && !running) {
      await refreshAfterSyncAction();
      router.refresh();
    }
    wasRunning.current = running;
  }, [router]);

  // Check now, then every 2.5 s while a sync runs and every minute otherwise.
  useEffect(() => {
    const first = setTimeout(poll, 0);
    const id = setInterval(poll, s?.run?.running ? 2500 : 60000);
    return () => { clearTimeout(first); clearInterval(id); };
  }, [poll, s?.run?.running]);

  const run = s?.run;
  const running = run?.running ?? false;
  const boxes = run?.mailboxes ?? [];
  const current = boxes.findIndex((m) => m.status === "scanning");
  const total = boxes.length;
  const lastRunErrors = !running && run?.finished_at ? boxes.filter((m) => m.status === "error").length : 0;

  const sync = () =>
    start(async () => {
      setError(undefined);
      const res = await startSyncAction([]);
      if (res.error) setError(res.error);
      else {
        wasRunning.current = true;
        await poll();
      }
    });

  return (
    <div className={styles.box}>
      <button type="button" className={styles.btn} onClick={sync} disabled={pending || running || !s || s.selected === 0}
        title={s && s.selected === 0 ? "Connect and select a mailbox in Profile first" : "Scan your selected mailboxes"}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"
          strokeLinejoin="round" aria-hidden="true" className={running ? styles.spin : undefined}>
          <path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" />
        </svg>
        <span className={styles.label}>{running ? "Syncing…" : "Sync now"}</span>
      </button>
      <p className={styles.meta} aria-live="polite">
        {running && current >= 0
          ? `Scanning ${boxes[current].address.split("@")[0]}… ${current + 1} of ${total}`
          : s?.error && !s.run
            ? s.error
            : lastRunErrors > 0
              ? `${lastRunErrors} mailbox${lastRunErrors > 1 ? "es" : ""} failed`
              : `Synced ${ago(s?.lastSyncedAt ?? null)}`}
      </p>
      {error && <p role="alert" className={styles.error}>{error}</p>}
      <Link href="/profile#mailboxes" className={styles.link}>
        {s ? `${s.selected} of ${s.connected} mailboxes` : "Mailboxes"}
      </Link>
    </div>
  );
}
