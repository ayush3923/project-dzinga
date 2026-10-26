"use client";

import { useState, useTransition, type KeyboardEvent } from "react";
import type { Intent, Situation } from "@/lib/api";
import { saveIntentAction } from "../actions";
import styles from "./intent.module.css";

const SITUATIONS: { id: Exclude<Situation, "">; title: string; hint: string }[] = [
  { id: "exploring", title: "Just exploring", hint: "Happy where I am, open to the right thing" },
  { id: "immediate", title: "Ready to join", hint: "Looking actively, can start soon" },
  { id: "reentry", title: "Returning to work", hint: "Coming back after a career break" },
  { id: "growth", title: "Growth move", hint: "Looking for a bigger role or scope" },
];

const LEVELS = ["Associate", "Senior", "Lead", "Manager", "Senior Manager", "Director", "VP"];
const PLACES = ["Bengaluru", "Mumbai", "Delhi NCR", "Hyderabad", "Pune", "Chennai", "Remote (India)", "United States"];
const COMPANY_TYPES = [
  { id: "startup", label: "Early-stage startup" },
  { id: "scaleup", label: "Scale-up" },
  { id: "enterprise", label: "Indian enterprise" },
  { id: "mnc", label: "Global MNC" },
  { id: "public", label: "Public sector" },
  { id: "nonprofit", label: "Non-profit" },
];

const STEPS = ["Situation", "Role", "Location", "Salary", "Company"] as const;

// Salaries are stored in whole currency units; people think in LPA (INR) or $k (USD).
const unit = (c: string) => (c === "USD" ? 1_000 : 100_000);
const unitLabel = (c: string) => (c === "USD" ? "$k / year" : "LPA");

function isSet(i: Intent) {
  return Boolean(i.situation || i.roles?.length || i.locations?.length);
}

export function IntentWizard({ initial }: { initial: Intent }) {
  const [intent, setIntent] = useState<Intent>({
    ...initial,
    roles: initial.roles ?? [],
    locations: initial.locations ?? [],
    company_types: initial.company_types ?? [],
    currency: initial.currency || "INR",
  });
  const [editing, setEditing] = useState(!isSet(initial));
  const [step, setStep] = useState(0);
  const [error, setError] = useState<string>();
  const [pending, start] = useTransition();

  const set = (patch: Partial<Intent>) => setIntent((i) => ({ ...i, ...patch }));
  const toggle = (key: "locations" | "company_types", v: string) =>
    set({ [key]: intent[key]!.includes(v) ? intent[key]!.filter((x) => x !== v) : [...intent[key]!, v] });

  const save = () =>
    start(async () => {
      const res = await saveIntentAction(intent);
      setError(res.error);
      if (!res.error) setEditing(false);
    });

  if (!editing) return <Summary intent={intent} onEdit={(s) => { setStep(s); setEditing(true); }} />;

  const last = step === STEPS.length - 1;
  return (
    <section className={`card ${styles.wizard}`} aria-labelledby="step-title">
      <ol className={styles.progress} aria-label="Steps">
        {STEPS.map((s, i) => (
          <li key={s} aria-current={i === step ? "step" : undefined} data-done={i < step}>
            <button type="button" onClick={() => setStep(i)}>{s}</button>
          </li>
        ))}
      </ol>

      <div className={styles.body}>
        {step === 0 && (
          <>
            <h2 id="step-title">Where are you right now?</h2>
            <div className={styles.options} role="radiogroup" aria-labelledby="step-title">
              {SITUATIONS.map((s) => (
                <button key={s.id} type="button" role="radio" aria-checked={intent.situation === s.id}
                  className={styles.option} onClick={() => set({ situation: s.id })}>
                  <strong>{s.title}</strong>
                  <span className="muted">{s.hint}</span>
                </button>
              ))}
            </div>
          </>
        )}

        {step === 1 && (
          <>
            <h2 id="step-title">Which roles?</h2>
            <TagInput label="Target roles" placeholder="e.g. Product Manager, then Enter"
              values={intent.roles!} onChange={(roles) => set({ roles })} />
            <p className={styles.label}>Seniority</p>
            <div className={styles.chips}>
              {LEVELS.map((l) => (
                <button key={l} type="button" className="chip" aria-pressed={intent.designation === l}
                  onClick={() => set({ designation: intent.designation === l ? "" : l })}>{l}</button>
              ))}
            </div>
          </>
        )}

        {step === 2 && (
          <>
            <h2 id="step-title">Where would you work?</h2>
            <div className={styles.chips}>
              {PLACES.map((p) => (
                <button key={p} type="button" className="chip" aria-pressed={intent.locations!.includes(p)}
                  onClick={() => toggle("locations", p)}>{p}</button>
              ))}
            </div>
            <TagInput label="Other places" placeholder="Add a city, then Enter"
              values={intent.locations!.filter((l) => !PLACES.includes(l))}
              onChange={(extra) => set({ locations: [...intent.locations!.filter((l) => PLACES.includes(l)), ...extra] })} />
          </>
        )}

        {step === 3 && (
          <>
            <h2 id="step-title">What pay range?</h2>
            <div className={styles.chips} role="group" aria-label="Currency">
              {["INR", "USD"].map((c) => (
                <button key={c} type="button" className="chip" aria-pressed={intent.currency === c}
                  onClick={() => set({ currency: c, salary_min: 0, salary_max: 0 })}>{c}</button>
              ))}
            </div>
            <div className={styles.salary}>
              {(["salary_min", "salary_max"] as const).map((k) => (
                <label key={k}>
                  <span className={styles.label}>{k === "salary_min" ? "At least" : "Ideally up to"}</span>
                  <span className={styles.money}>
                    <input className="input" type="number" min={0} inputMode="decimal"
                      value={intent[k] ? intent[k] / unit(intent.currency) : ""}
                      onChange={(e) => set({ [k]: Math.round(Number(e.target.value || 0) * unit(intent.currency)) })} />
                    <span className="faint">{unitLabel(intent.currency)}</span>
                  </span>
                  {intent[k] > 0 && (
                    <span className={styles.label}>= {intent.currency === "USD" ? "$" : "₹"}{intent[k].toLocaleString(intent.currency === "USD" ? "en-US" : "en-IN")} a year</span>
                  )}
                </label>
              ))}
            </div>
            {(intent.currency === "USD" ? intent.salary_max > 10_000_000 : intent.salary_max > 500_000_000) && (
              <p role="alert" className={styles.error}>
                That’s over {intent.currency === "USD" ? "$10M" : "₹50 crore"} a year. Enter {intent.currency === "USD" ? "thousands of dollars (e.g. 180)" : "lakhs per annum (e.g. 60)"}, not the full amount.
              </p>
            )}
          </>
        )}

        {step === 4 && (
          <>
            <h2 id="step-title">What kind of company?</h2>
            <div className={styles.chips}>
              {COMPANY_TYPES.map((c) => (
                <button key={c.id} type="button" className="chip" aria-pressed={intent.company_types!.includes(c.id)}
                  onClick={() => toggle("company_types", c.id)}>{c.label}</button>
              ))}
            </div>
          </>
        )}
      </div>

      {error && <p role="alert" className={styles.error}>{error}</p>}
      <div className={styles.foot}>
        <button type="button" className="btn" onClick={() => (step ? setStep(step - 1) : isSet(initial) && setEditing(false))}
          disabled={step === 0 && !isSet(initial)}>
          {step === 0 ? "Cancel" : "Back"}
        </button>
        {last ? (
          <button type="button" className="btn btn-accent" onClick={save} disabled={pending}>
            {pending ? "Saving…" : "Save intent"}
          </button>
        ) : (
          <button type="button" className="btn btn-primary" onClick={() => setStep(step + 1)}>Continue</button>
        )}
      </div>
    </section>
  );
}

function TagInput({ label, placeholder, values, onChange }: {
  label: string; placeholder: string; values: string[]; onChange: (v: string[]) => void;
}) {
  const [draft, setDraft] = useState("");
  const add = () => {
    const v = draft.trim().slice(0, 100);
    if (v && !values.includes(v) && values.length < 10) onChange([...values, v]);
    setDraft("");
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter" || e.key === ",") { e.preventDefault(); add(); }
    if (e.key === "Backspace" && !draft && values.length) onChange(values.slice(0, -1));
  };
  return (
    <div>
      <label className={styles.label}>
        {label}
        <div className={styles.tags}>
          {values.map((v) => (
            <span key={v} className={styles.tag}>
              {v}
              <button type="button" aria-label={`Remove ${v}`} onClick={() => onChange(values.filter((x) => x !== v))}>×</button>
            </span>
          ))}
          <input value={draft} onChange={(e) => setDraft(e.target.value)} onKeyDown={onKey} onBlur={add}
            placeholder={values.length ? "" : placeholder} maxLength={100} />
        </div>
      </label>
    </div>
  );
}

function Summary({ intent, onEdit }: { intent: Intent; onEdit: (step: number) => void }) {
  const sit = SITUATIONS.find((s) => s.id === intent.situation);
  const u = unit(intent.currency);
  const pay = intent.salary_min || intent.salary_max
    ? `${intent.salary_min ? intent.salary_min / u : "?"}–${intent.salary_max ? intent.salary_max / u : "?"} ${unitLabel(intent.currency)}`
    : "Not set";
  const rows: [string, string][] = [
    ["Situation", sit?.title ?? "Not set"],
    ["Role", [intent.designation, intent.roles?.join(", ")].filter(Boolean).join(" · ") || "Not set"],
    ["Location", intent.locations?.join(", ") || "Not set"],
    ["Salary", pay],
    ["Company", intent.company_types?.map((c) => COMPANY_TYPES.find((t) => t.id === c)?.label ?? c).join(", ") || "Any"],
  ];
  return (
    <section className={`card ${styles.summary}`} aria-label="Your intent">
      {rows.map(([k, v], i) => (
        <button key={k} type="button" className={styles.sumRow} onClick={() => onEdit(i)}>
          <span className="faint">{k}</span>
          <span>{v}</span>
          <span className={styles.edit} aria-hidden="true">Edit</span>
        </button>
      ))}
    </section>
  );
}
