"use client";

import { useState, useTransition } from "react";
import { diagnoseAction } from "../actions";
import styles from "./organizations.module.css";

export function OrgAgentButton({ id, label, quiet }: { id: number; label: string; quiet?: boolean }) {
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  const run = () =>
    start(async () => {
      const res = await diagnoseAction(id);
      setError(res.error);
    });
  return (
    <>
      <button type="button" className={quiet ? styles.link : "btn btn-primary"} onClick={run} disabled={pending}>
        {pending ? "Diagnosing…" : label}
      </button>
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </>
  );
}
