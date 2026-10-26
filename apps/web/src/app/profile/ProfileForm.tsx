"use client";

import { useState, useTransition } from "react";
import type { Experience, Profile } from "@/lib/api";
import { saveProfileAction } from "../actions";
import styles from "./profile.module.css";

const blank: Experience = { title: "", company: "", start_month: "", end_month: "", location: "", highlights: [] };

export function ProfileForm({ initial }: { initial: Profile }) {
  const [p, setP] = useState<Profile>(initial);
  const [skills, setSkills] = useState(initial.skills.join(", "));
  const [pending, start] = useTransition();
  const [status, setStatus] = useState<{ ok?: boolean; error?: string }>({});

  const set = (patch: Partial<Profile>) => setP((x) => ({ ...x, ...patch }));
  const setExp = (i: number, patch: Partial<Experience>) =>
    set({ experiences: p.experiences.map((e, j) => (j === i ? { ...e, ...patch } : e)) });

  const save = () =>
    start(async () => {
      const res = await saveProfileAction({ ...p, skills: skills.split(",").map((s) => s.trim()).filter(Boolean) });
      setStatus(res.error ? { error: res.error } : { ok: true });
    });

  const field = (label: string, key: "full_name" | "headline" | "current_title" | "current_company" | "location") => (
    <label className={styles.field}>
      <span>{label}</span>
      <input className="input" value={p[key]} onChange={(e) => set({ [key]: e.target.value })} />
    </label>
  );

  return (
    <form className={styles.form} onSubmit={(e) => { e.preventDefault(); save(); }}>
      <section className={`card ${styles.card}`}>
        <h2>Basics</h2>
        <div className={styles.grid}>
          {field("Full name", "full_name")}
          {field("Headline", "headline")}
          {field("Current title", "current_title")}
          {field("Current company", "current_company")}
          {field("Location", "location")}
          <label className={styles.field}>
            <span>Total years of experience</span>
            <input className="input" type="number" min={0} max={60} step={0.5} value={p.total_years || ""}
              onChange={(e) => set({ total_years: Number(e.target.value || 0) })} />
          </label>
        </div>
        <label className={styles.field}>
          <span>Summary</span>
          <textarea className="input" rows={4} value={p.summary} onChange={(e) => set({ summary: e.target.value })} />
        </label>
        <label className={styles.field}>
          <span>Skills (comma-separated)</span>
          <input className="input" value={skills} onChange={(e) => setSkills(e.target.value)} />
        </label>
      </section>

      <section className={`card ${styles.card}`}>
        <h2>Experience <span className="faint">most recent first</span></h2>
        {p.experiences.map((e, i) => (
          <details key={i} className={styles.exp} open={!e.title}>
            <summary>
              <strong>{e.title || "New role"}</strong>
              <span className="muted">{e.company}</span>
              <span className="faint">{e.start_month}{e.start_month ? ` – ${e.end_month || "present"}` : ""}</span>
            </summary>
            <div className={styles.grid}>
              <label className={styles.field}><span>Title</span>
                <input className="input" value={e.title} onChange={(ev) => setExp(i, { title: ev.target.value })} /></label>
              <label className={styles.field}><span>Company</span>
                <input className="input" value={e.company} onChange={(ev) => setExp(i, { company: ev.target.value })} /></label>
              <label className={styles.field}><span>Start</span>
                <input className="input" type="month" value={e.start_month} onChange={(ev) => setExp(i, { start_month: ev.target.value })} /></label>
              <label className={styles.field}><span>End (blank = present)</span>
                <input className="input" type="month" value={e.end_month} onChange={(ev) => setExp(i, { end_month: ev.target.value })} /></label>
            </div>
            <label className={styles.field}>
              <span>Achievements (one per line, with numbers where you have them)</span>
              <textarea className="input" rows={4} value={e.highlights.join("\n")}
                onChange={(ev) => setExp(i, { highlights: ev.target.value.split("\n") })} />
            </label>
            <button type="button" className={styles.link}
              onClick={() => set({ experiences: p.experiences.filter((_, j) => j !== i) })}>Remove this role</button>
          </details>
        ))}
        <button type="button" className="btn" onClick={() => set({ experiences: [...p.experiences, { ...blank }] })}>
          Add a role
        </button>
      </section>

      <div className={styles.foot}>
        {status.error && <p role="alert" className={styles.error}>{status.error}</p>}
        {status.ok && <p className="muted">Saved.</p>}
        <button type="submit" className="btn btn-accent" disabled={pending}>{pending ? "Saving…" : "Save profile"}</button>
      </div>
    </form>
  );
}
