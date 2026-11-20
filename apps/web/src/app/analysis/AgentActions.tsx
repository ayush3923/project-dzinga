"use client";

import { useEffect, useState, useTransition } from "react";
import type { AppDetail } from "@/lib/api";
import { draftEmailAction, draftsSeenAction, runFitAction } from "../actions";
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

/** Marks a new draft as opened, which clears the tile's "ready" icon. */
function SeenOnView({ id }: { id: number }) {
  useEffect(() => { void draftsSeenAction(id); }, [id]);
  return null;
}

/**
 * Cold email. Asking queues it for Claude, who drafts it on your next run (in the
 * Claude app, at no API cost); the tile shows a clock until then and an envelope
 * once it's ready. Nothing is ever sent.
 */
export function DraftPanel({ id, company, contacts, draft, queuedAt, queuedFor, disabled }: {
  id: number;
  company: string;
  contacts: { id: number; label: string }[];
  draft: Draft | null;
  queuedAt: string | null;
  queuedFor: string;
  disabled: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [to, setTo] = useState<string>("");
  const [who, setWho] = useState("");
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  const [copied, setCopied] = useState(false);

  const queue = () =>
    start(async () => {
      const res = await draftEmailAction(id, to ? Number(to) : null, to ? "" : who);
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
      {queuedAt && (
        <p className={styles.queued}>
          <span aria-hidden="true">◷</span>
          Queued{queuedFor && <> for <strong>{queuedFor}</strong></>} on {new Date(queuedAt).toLocaleDateString("en-IN", { day: "numeric", month: "short" })}.
          Claude drafts it on your next refresh run.
        </p>
      )}
      {!draft && !open && !queuedAt && (
        <p className="muted">
          Claude drafts a short email (under 100 words) to the person you choose, written to be read and answered:
          a specific subject, one proof point from your work and one easy question. Nothing is sent.
        </p>
      )}
      {(open || (!draft && !queuedAt)) && (
        <div className={styles.draftForm}>
          <label>
            <span className="faint">Send to</span>
            <select className="input" value={to} onChange={(e) => setTo(e.target.value)} disabled={pending}>
              <option value="">Someone else (type below)</option>
              {contacts.map((c) => <option key={c.id} value={c.id}>{c.label}</option>)}
            </select>
          </label>
          {!to && (
            <label>
              <span className="faint">Who is it for?</span>
              <input className="input" value={who} maxLength={120} onChange={(e) => setWho(e.target.value)} disabled={pending}
                placeholder={`e.g. Priya Sharma, HR at ${company}`} />
            </label>
          )}
          <button type="button" className="btn btn-accent" onClick={queue} disabled={pending || disabled}>
            {pending ? "Queuing…" : "Draft cold email"}
          </button>
          {disabled && <span className="faint">Add your profile first.</span>}
        </div>
      )}
      {error && <p role="alert" className={styles.error}>{error}</p>}

      {draft && (
        <article className={styles.email}>
          {draft.new && <SeenOnView id={id} />}
          <header>
            <span className="faint">{draft.recipient ? `To ${draft.recipient} · ` : ""}Subject</span>
            <strong>{draft.subject}</strong>
          </header>
          <p className={styles.body}>{draft.body}</p>
          <div className={styles.emailActions}>
            <button type="button" className="btn" onClick={() => copy(draft)}>{copied ? "Copied" : "Copy email"}</button>
            {!open && !queuedAt && <button type="button" className={styles.link} onClick={() => setOpen(true)}>Draft another</button>}
          </div>
          {draft.product_analysis && (
            <details className={styles.more}>
              <summary>Product analysis</summary>
              <p>{draft.product_analysis}</p>
            </details>
          )}
          {draft.experience_points.length > 0 && (
            <details className={styles.more}>
              <summary>Your experience pointers ({draft.experience_points.length})</summary>
              <ul>{draft.experience_points.map((t, i) => <li key={i}>{t}</li>)}</ul>
            </details>
          )}
          {draft.value_adds.length > 0 && (
            <details className={styles.more}>
              <summary>Value you could add ({draft.value_adds.length})</summary>
              <ul>{draft.value_adds.map((t, i) => <li key={i}>{t}</li>)}</ul>
            </details>
          )}
          {draft.improvement_ideas.length > 0 && (
            <details className={styles.more}>
              <summary>Product improvement ideas ({draft.improvement_ideas.length})</summary>
              <ul>{draft.improvement_ideas.map((t, i) => <li key={i}>{t}</li>)}</ul>
            </details>
          )}
          <p className="faint">Check every claim before sending. Drafted by {draft.model}.</p>
        </article>
      )}
    </div>
  );
}
