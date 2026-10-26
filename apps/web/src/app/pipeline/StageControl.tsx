"use client";

import { useState, useTransition } from "react";
import { STAGES, STAGE_META } from "@/lib/format";
import { setStageAction } from "../actions";
import styles from "./pipeline.module.css";

/** Lets the user correct a stage the classifier got wrong. Hidden until asked for. */
export function StageControl({ id, stage, manual }: { id: number; stage: string; manual: boolean }) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string>();
  const [pending, start] = useTransition();

  const save = (next: string) =>
    start(async () => {
      const res = await setStageAction(id, next);
      setError(res.error);
      if (!res.error) setOpen(false);
    });

  if (!open) {
    return (
      <p className={styles.correct}>
        {manual ? "You set this stage manually." : "Stage detected from your emails."}{" "}
        <button type="button" className={styles.link} onClick={() => setOpen(true)}>
          {manual ? "Change" : "Not right?"}
        </button>
      </p>
    );
  }
  return (
    <div className={styles.stagePicker} aria-busy={pending}>
      <div role="group" aria-label="Set stage" className={styles.chipsWrap}>
        {STAGES.map((s) => (
          <button key={s} type="button" className="chip" data-stage={s} aria-pressed={s === stage}
            disabled={pending} onClick={() => save(s)}>
            <span className="dot" />
            {STAGE_META[s].label}
          </button>
        ))}
      </div>
      <div className={styles.pickerFoot}>
        {manual && (
          <button type="button" className={styles.link} disabled={pending} onClick={() => save("")}>
            Use detected stage
          </button>
        )}
        <button type="button" className={styles.link} onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </div>
  );
}
