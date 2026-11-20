"use client";

import { useState, useTransition } from "react";
import { diagnoseAction } from "../actions";
import styles from "./organizations.module.css";

/**
 * Diagnoses are written by Claude in the app (not through the API, to save cost):
 * this queues one. Each refresh also queues five organizations at random.
 */
export function DiagnoseButton({ id, label, requestedAt, quiet }: { id: number; label: string; requestedAt: string | null; quiet?: boolean }) {
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  if (requestedAt) {
    return (
      <span className="faint">
        Diagnosis queued for Claude {new Date(requestedAt).toLocaleDateString("en-IN", { day: "numeric", month: "short" })}.
      </span>
    );
  }
  const run = () =>
    start(async () => {
      const res = await diagnoseAction(id);
      setError(res.error);
    });
  return (
    <>
      <button type="button" className={quiet ? styles.link : "btn btn-primary"} onClick={run} disabled={pending}>
        {pending ? "Queuing…" : label}
      </button>
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </>
  );
}
