"use client";

import { useState, useTransition } from "react";
import { roundsLabel } from "@/lib/format";
import { saveRoundsAction } from "../actions";
import styles from "./pipeline.module.css";

/** Records interview progress, which emails can't show reliably. Hidden until asked for. */
export function RoundsControl({ id, stage, rounds, next }: { id: number; stage: string; rounds: number; next: string }) {
  const label = roundsLabel(rounds, next, stage);
  const [open, setOpen] = useState(false);
  const [r, setR] = useState(rounds);
  const [n, setN] = useState(next);
  const [error, setError] = useState<string>();
  const [pending, start] = useTransition();

  const save = () =>
    start(async () => {
      const res = await saveRoundsAction(id, r, n);
      setError(res.error);
      if (!res.error) setOpen(false);
    });

  if (!open) {
    if (!label && stage !== "interview" && stage !== "offer") return null;
    return (
      <p className={styles.correct}>
        {label ? <strong className={styles.rounds}>{label}</strong> : "No interview rounds recorded."}{" "}
        <button type="button" className={styles.link} onClick={() => setOpen(true)}>
          {label ? "Update" : "Add rounds"}
        </button>
      </p>
    );
  }
  return (
    <div className={styles.stagePicker} aria-busy={pending}>
      <div className={styles.roundsForm}>
        <label>
          Rounds done
          <input className="input" type="number" min={0} max={20} step={1} value={r} disabled={pending}
            onChange={(e) => setR(e.target.valueAsNumber)} />
        </label>
        <label>
          What’s next
          <input className="input" maxLength={120} value={n} disabled={pending} placeholder="e.g. Round 2 interview"
            autoComplete="off" onChange={(e) => setN(e.target.value)} />
        </label>
      </div>
      <div className={styles.pickerFoot}>
        <button type="button" className={styles.link} disabled={pending} onClick={save}>Save</button>
        <button type="button" className={styles.link} onClick={() => setOpen(false)}>Cancel</button>
      </div>
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </div>
  );
}
