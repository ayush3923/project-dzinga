"use client";

import { useRouter } from "next/navigation";
import { useState, useTransition } from "react";
import { editApplicationAction, runFitAction } from "@/app/actions";
import styles from "./EditDetails.module.css";

/**
 * Lets the user correct what the sync couldn't infer: organization, role and a
 * public link to the job description. A new link is scored against the intent and
 * CV straight away.
 */
export function EditDetails({ id, company, role, jobUrl, basePath }: {
  id: number;
  company: string;
  role: string;
  jobUrl: string;
  basePath: string; // e.g. "/analysis?view=all&app=" — where to go if the application is merged
}) {
  const router = useRouter();
  const unknownCompany = company === "Unknown company";
  const needsAttention = unknownCompany || !role || !jobUrl;
  const [open, setOpen] = useState(false);
  const [c, setC] = useState(unknownCompany ? "" : company);
  const [r, setR] = useState(role);
  const [u, setU] = useState(jobUrl);
  const [pending, start] = useTransition();
  const [scoring, setScoring] = useState(false);
  const [msg, setMsg] = useState<{ ok?: string; error?: string }>({});

  const save = () =>
    start(async () => {
      setMsg({});
      const res = await editApplicationAction(id, c, r, u);
      if (res.error || !res.id) {
        setMsg({ error: res.error ?? "Couldn’t save." });
        return;
      }
      setOpen(false);
      if (res.id !== id) router.replace(basePath + res.id, { scroll: false });
      if (u.trim() && u.trim() !== jobUrl) {
        setScoring(true);
        setMsg({ ok: "Saved. Scoring this role against your intent and CV… (1–3 minutes)" });
        const fit = await runFitAction(res.id);
        setScoring(false);
        setMsg(fit.error ? { error: `Saved, but the fit score failed: ${fit.error}` } : { ok: "Saved and scored." });
      } else {
        setMsg({ ok: "Saved." });
      }
    });

  const missing = [unknownCompany && "organization", !role && "role", !jobUrl && "job description link"].filter(Boolean);

  return (
    <div className={styles.box}>
      {!open && (
        needsAttention ? (
          <div className={styles.prompt}>
            <span>Couldn’t detect the {missing.join(", ").replace(/, ([^,]*)$/, " or $1")} from your emails.</span>
            <button type="button" className="btn" onClick={() => setOpen(true)} disabled={scoring}>Add details</button>
          </div>
        ) : (
          <button type="button" className={styles.link} onClick={() => setOpen(true)} disabled={scoring}>Edit details</button>
        )
      )}
      {open && (
        <form className={styles.form} onSubmit={(e) => { e.preventDefault(); save(); }}>
          <label className={styles.field}>
            <span>Organization</span>
            <input className="input" value={c} onChange={(e) => setC(e.target.value)} maxLength={120} required
              placeholder="e.g. Accenture" autoFocus />
          </label>
          <label className={styles.field}>
            <span>Job role</span>
            <input className="input" value={r} onChange={(e) => setR(e.target.value)} maxLength={200}
              placeholder="e.g. Principal Product Manager" />
          </label>
          <label className={styles.field}>
            <span>Public link to the job description</span>
            <input className="input" type="url" value={u} onChange={(e) => setU(e.target.value)} maxLength={2000}
              placeholder="https://…" pattern="https://.*" />
            <small className="faint">A new link is scored against your intent and CV right away (uses Claude, about $0.15–0.40).</small>
          </label>
          <div className={styles.actions}>
            <button type="submit" className="btn btn-primary" disabled={pending}>{pending ? "Saving…" : "Save"}</button>
            <button type="button" className={styles.link} onClick={() => setOpen(false)}>Cancel</button>
          </div>
        </form>
      )}
      {msg.ok && <p className={styles.ok} aria-live="polite">{msg.ok}</p>}
      {msg.error && <p role="alert" className={styles.error}>{msg.error}</p>}
    </div>
  );
}
