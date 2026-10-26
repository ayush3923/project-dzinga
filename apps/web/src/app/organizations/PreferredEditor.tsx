"use client";

import { useState, useTransition } from "react";
import { savePreferredAction } from "../actions";
import styles from "./organizations.module.css";

/** Edit preferred organizations: pick from your companies or the Fortune 500 list, or type any name. */
export function PreferredEditor({ mode, current, suggestions }: { mode: "auto" | "manual"; current: string[]; suggestions: string[] }) {
  const [open, setOpen] = useState(false);
  const [list, setList] = useState(current);
  const [draft, setDraft] = useState("");
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();

  const add = () => {
    const v = draft.trim().slice(0, 120);
    if (v && !list.some((x) => x.toLowerCase() === v.toLowerCase()) && list.length < 20) setList([...list, v]);
    setDraft("");
  };
  const save = (auto: boolean) =>
    start(async () => {
      const res = await savePreferredAction(auto ? [] : list, auto);
      setError(res.error);
      if (!res.error) setOpen(false);
    });

  if (!open) {
    return (
      <button type="button" className={styles.link} onClick={() => { setList(current); setOpen(true); }}>
        Edit preferred organizations
      </button>
    );
  }
  return (
    <div className={`card ${styles.editor}`}>
      <div className={styles.chips}>
        {list.map((n, i) => (
          <span key={n} className={styles.tag}>
            {n}
            <button type="button" aria-label={`Move ${n} up`} disabled={i === 0}
              onClick={() => setList(list.map((x, j) => (j === i - 1 ? n : j === i ? list[i - 1] : x)))}>↑</button>
            <button type="button" aria-label={`Remove ${n}`} onClick={() => setList(list.filter((x) => x !== n))}>×</button>
          </span>
        ))}
        {list.length === 0 && <span className="faint">No preferred organizations</span>}
      </div>
      <div className={styles.addRow}>
        <label className="visually-hidden" htmlFor="org-add">Add an organization</label>
        <input id="org-add" className="input" list="org-suggestions" value={draft} maxLength={120}
          placeholder="Type a company: your organizations and Fortune 500 suggestions appear"
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }} />
        <datalist id="org-suggestions">
          {suggestions.map((s) => <option key={s} value={s} />)}
        </datalist>
        <button type="button" className="btn" onClick={add} disabled={!draft.trim()}>Add</button>
      </div>
      <p className="faint">Suggestions include a partial Fortune 500 list. Any other company can be typed in.</p>
      {error && <p role="alert" className={styles.error}>{error}</p>}
      <div className={styles.editorFoot}>
        {mode === "manual" && (
          <button type="button" className={styles.link} disabled={pending} onClick={() => save(true)}>
            Reset to my top 3
          </button>
        )}
        <span style={{ flex: 1 }} />
        <button type="button" className="btn" onClick={() => setOpen(false)} disabled={pending}>Cancel</button>
        <button type="button" className="btn btn-primary" onClick={() => save(false)} disabled={pending}>
          {pending ? "Saving…" : "Save"}
        </button>
      </div>
    </div>
  );
}
