"use client";

import { useState, useTransition } from "react";
import { addInterviewNoteAction } from "@/app/actions";
import styles from "./pipeline.module.css";

/** Notes on a round: how it went, the questions, any feedback. Saving queues Claude's review. */
export function InterviewNoteForm({ id, round }: { id: number; round: number }) {
  const [open, setOpen] = useState(false);
  const [r, setR] = useState(round);
  const [body, setBody] = useState("");
  const [error, setError] = useState<string>();
  const [pending, start] = useTransition();

  const save = () =>
    start(async () => {
      const res = await addInterviewNoteAction(id, Number.isFinite(r) ? r : 0, body);
      setError(res.error);
      if (!res.error) {
        setBody("");
        setOpen(false);
      }
    });

  if (!open) {
    return (
      <button type="button" className={styles.link} onClick={() => setOpen(true)}>
        Add notes on a round
      </button>
    );
  }
  return (
    <div className={styles.noteForm} aria-busy={pending}>
      <label className={styles.noteRound}>
        Round
        <input className="input" type="number" min={0} max={20} step={1} value={r} disabled={pending}
          onChange={(e) => setR(e.target.valueAsNumber)} />
      </label>
      <label>
        <span className="visually-hidden">Your notes</span>
        <textarea className="input" rows={6} maxLength={4000} value={body} disabled={pending} autoFocus
          placeholder="Who you met, what they asked, how your answers landed, any feedback or signals, how you felt it went…"
          onChange={(e) => setBody(e.target.value)} />
      </label>
      <p className="faint">Claude reads these with the job description, your fit score and profile, and comments on your chances on its next run.</p>
      <div className={styles.pickerFoot}>
        <button type="button" className={styles.link} disabled={pending || body.trim().length < 10} onClick={save}>
          {pending ? "Saving…" : "Save and queue for Claude"}
        </button>
        <button type="button" className={styles.link} onClick={() => setOpen(false)}>Cancel</button>
      </div>
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </div>
  );
}
