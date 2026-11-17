"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState, useTransition } from "react";
import { refreshAfterSyncAction, startSyncAction, syncStatusAction, type SyncSummary } from "@/app/actions";
import { refreshStamp } from "@/lib/format";
import styles from "./SyncButton.module.css";

/** Top right of every page: when the app last refreshed, and a button that scans the selected mailboxes and refreshes every page. */
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

  const status = running && current >= 0
    ? `Scanning ${boxes[current].address.split("@")[0]}… ${current + 1} of ${total}`
    : running
      ? "Refreshing…"
      : s?.error && !s.run
        ? s.error
        : `Last refresh ${refreshStamp(s?.lastSyncedAt ?? null)}`;

  return (
    <div className={styles.box}>
      <p className={styles.meta} aria-live="polite">{s ? status : "\u00a0"}</p>
      {lastRunErrors > 0 && (
        <Link href="/profile#mailboxes" className={styles.error}>
          {lastRunErrors} mailbox{lastRunErrors > 1 ? "es" : ""} failed
        </Link>
      )}
      <button type="button" className={styles.btn} onClick={sync} disabled={pending || running || !s || s.selected === 0}
        aria-label="Refresh"
        title={s && s.selected === 0 ? "Connect and select a mailbox in Profile first" : `Refresh: scan ${s?.selected ?? 0} of ${s?.connected ?? 0} mailboxes`}>
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"
          strokeLinejoin="round" aria-hidden="true" className={running ? styles.spin : undefined}>
          <path d="M21 12a9 9 0 1 1-2.64-6.36M21 3v6h-6" />
        </svg>
        <span className={styles.label}>Refresh</span>
      </button>
      {error && <p role="alert" className={styles.alert}>{error}</p>}
    </div>
  );
}
