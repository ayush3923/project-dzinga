"use client";

import { useState, useTransition } from "react";
import type { AppDetail } from "@/lib/api";
import { draftEmailAction, runFitAction } from "../actions";
import styles from "./analysis.module.css";

export function FitButton({ id, label, quiet }: { id: number; label: string; quiet?: boolean }) {
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  return (
    <>
      <button type="button" className={quiet ? styles.link : "btn btn-primary"} disabled={pending}
        onClick={() => start(async () => setError((await runFitAction(id)).error))}>
        {pending ? "Reading the posting and scoring…" : label}
      </button>
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </>
  );
}

type Draft = AppDetail["drafts"][number];

export function DraftPanel({ id, company, contacts, draft, disabled }: {
  id: number;
  company: string;
  contacts: { id: number; label: string }[];
  draft: Draft | null;
  disabled: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [to, setTo] = useState<string>("");
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  const [copied, setCopied] = useState(false);

  const generate = () =>
    start(async () => {
      const res = await draftEmailAction(id, to ? Number(to) : null);
      setError(res.error);
      if (!res.error) setOpen(false);
    });

  const copy = async (d: Draft) => {
    try {
      await navigator.clipboard.writeText(`Subject: ${d.subject}\n\n${d.body}`);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setError("Couldn’t copy. Select the text and copy it manually.");
    }
  };

  return (
    <div className={styles.draft}>
      {!draft && !open && (
        <p className="muted">
          On request, Claude drafts a short email introducing you, with a read of the product, two of your experience
          points, the value you’d add and an idea to improve the product. Nothing is sent.
        </p>
      )}
      {(open || !draft) && (
        <div className={styles.draftForm}>
          <label>
            <span className="faint">Send to</span>
            <select className="input" value={to} onChange={(e) => setTo(e.target.value)} disabled={pending}>
              <option value="">{company} hiring team (no named person)</option>
              {contacts.map((c) => <option key={c.id} value={c.id}>{c.label}</option>)}
            </select>
          </label>
          <button type="button" className="btn btn-accent" onClick={generate} disabled={pending || disabled}>
            {pending ? "Drafting… (up to a minute)" : "Draft cold email"}
          </button>
          {disabled && <span className="faint">Add your profile first.</span>}
        </div>
      )}
      {error && <p role="alert" className={styles.error}>{error}</p>}

      {draft && (
        <article className={styles.email}>
          <header>
            <span className="faint">Subject</span>
            <strong>{draft.subject}</strong>
          </header>
          <p className={styles.body}>{draft.body}</p>
          <div className={styles.emailActions}>
            <button type="button" className="btn" onClick={() => copy(draft)}>{copied ? "Copied" : "Copy email"}</button>
            {!open && <button type="button" className={styles.link} onClick={() => setOpen(true)}>Draft another</button>}
          </div>
          <details className={styles.more}>
            <summary>Product analysis</summary>
            <p>{draft.product_analysis}</p>
          </details>
          <details className={styles.more}>
            <summary>Your experience pointers ({draft.experience_points.length})</summary>
            <ul>{draft.experience_points.map((t, i) => <li key={i}>{t}</li>)}</ul>
          </details>
          <details className={styles.more}>
            <summary>Value you could add ({draft.value_adds.length})</summary>
            <ul>{draft.value_adds.map((t, i) => <li key={i}>{t}</li>)}</ul>
          </details>
          <details className={styles.more}>
            <summary>Product improvement ideas ({draft.improvement_ideas.length})</summary>
            <ul>{draft.improvement_ideas.map((t, i) => <li key={i}>{t}</li>)}</ul>
          </details>
          <p className="faint">Check every claim before sending. Drafted by {draft.model}.</p>
        </article>
      )}
    </div>
  );
}
