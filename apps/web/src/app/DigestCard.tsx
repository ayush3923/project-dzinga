import Link from "next/link";
import type { Digest, EventKind, Suggestion } from "@/lib/api";
import { StageBadge } from "@/components/ui";
import styles from "./today.module.css";

const MODEL: Record<string, string> = { "claude-sonnet-5": "Claude Sonnet 5", template: "a template" };

function when(iso: string) {
  return new Date(iso).toLocaleString("en-IN", { timeZone: "Asia/Kolkata", weekday: "short", hour: "numeric", minute: "2-digit" });
}

/** What kind of step it is, from its wording: an interview, an assessment, a decision, or anything else. */
export function stepKind(title: string): "interview" | "assessment" | "decision" | "other" {
  const t = title.toLowerCase();
  if (/interview|round|call|panel|screen/.test(t)) return "interview";
  if (/assessment|assignment|test|case study/.test(t)) return "assessment";
  if (/decision|offer/.test(t)) return "decision";
  return "other";
}

const STEP_ICON = {
  interview: "M15 10l4.55-2.27A1 1 0 0 1 21 8.62v6.76a1 1 0 0 1-1.45.9L15 14M5 18h8a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2z",
  assessment: "M9 11l3 3L22 4M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11",
  decision: "M12 22c5.5 0 10-4.5 10-10S17.5 2 12 2 2 6.5 2 12s4.5 10 10 10zM12 6v6l4 2",
  other: "M5 12h14M12 5l7 7-7 7",
} as const;

/**
 * The morning digest, full width and short: the read beside this week's numbers,
 * then what's up next as tiles (the most useful thing on the page), then the facts
 * behind it, one click away.
 */
export function DigestCard({ d, suggested }: { d: Digest; suggested: Suggestion[] | null }) {
  const t = d.trends;
  // The next steps get their own tiles, so the prose stops before them.
  const read = d.text.split(/\s+Next:\s/)[0];
  return (
    <section className={`ai-frame ${styles.digest}`} aria-labelledby="digest-h">
      <div className={styles.digestTop}>
        <div className={styles.digestRead}>
          <h2 id="digest-h" className={styles.digestTitle}>
            <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 0c.4 3.9 2.1 5.6 6 6-3.9.4-5.6 2.1-6 6-.4-3.9-2.1-5.6-6-6 3.9-.4 5.6-2.1 6-6z" fill="#8b5cf6" /></svg>
            Morning digest
          </h2>
          <p className={styles.digestText}>{read}</p>
        </div>
        <dl className={styles.kpis} aria-label="This week">
          <div>
            <dt>Applied</dt>
            <dd>{t.apps_this_week}<small>vs {t.apps_last_week} last week</small></dd>
          </div>
          <div>
            <dt>Replies</dt>
            <dd>{t.responses_this_week}<small>this week</small></dd>
          </div>
          <div>
            <dt>Reply rate</dt>
            <dd>{Math.round(t.response_rate_30d * 100)}%<small>last 30 days</small></dd>
          </div>
        </dl>
      </div>

      {d.next_steps.length > 0 && (
        <div className={styles.upNextWrap}>
          <h3 className={styles.label}>Up next</h3>
          <ol className={styles.upNext}>
            {d.next_steps.slice(0, 3).map((s) => {
              const kind = stepKind(s.title);
              return (
                <li key={s.id}>
                  <Link href={`/pipeline?app=${s.id}`} className={styles.step} data-kind={kind}>
                    <span className={styles.stepIcon} aria-hidden="true">
                      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                        <path d={STEP_ICON[kind]} />
                      </svg>
                    </span>
                    <span className={styles.stepBody}>
                      <strong>{s.title}</strong>
                      <span className="faint">{s.company}</span>
                    </span>
                  </Link>
                </li>
              );
            })}
          </ol>
        </div>
      )}

      <details className={styles.more}>
        <summary>
          What’s behind it <span className="faint">· since {when(d.since)} · written by {MODEL[d.model] ?? d.model}</span>
        </summary>
        <div className={styles.facts}>
          <div>
            <h3>New applications · {d.new_applications.length}</h3>
            {d.new_applications.length === 0 ? <p className="faint">None since the last refresh.</p> : (
              <ul>{d.new_applications.map((a) => (
                <li key={a.id}><Link href={`/pipeline?app=${a.id}`}><strong>{a.company}</strong> <span className="muted">{a.role || "Role not detected"}</span></Link></li>
              ))}</ul>
            )}
          </div>
          <div>
            <h3>Replies · {d.responses.length}</h3>
            {d.responses.length === 0 ? <p className="faint">No replies since the last refresh.</p> : (
              <ul>{d.responses.map((a) => (
                <li key={a.id}><Link href={`/pipeline?app=${a.id}`}><StageBadge stage={(a.kind ?? "update") as EventKind} /> <strong>{a.company}</strong> <span className="muted">{a.role}</span></Link></li>
              ))}</ul>
            )}
          </div>
          <div>
            <h3>This week</h3>
            <p>{t.rejections_this_week} rejections · {t.waiting} waiting for a reply</p>
          </div>
          <div>
            <h3>Suggested companies</h3>
            {suggested === null ? <p className="faint">The organizations service isn’t reachable.</p> : suggested.length === 0 ? <p className="faint">Add preferred companies in Organizations to get suggestions.</p> : (
              <ul>{suggested.map((s) => (
                <li key={s.id}><Link href={`/organizations?org=${s.id}`}><strong>{s.name}</strong> <span className="muted">· {s.reason}</span></Link></li>
              ))}</ul>
            )}
          </div>
        </div>
      </details>
    </section>
  );
}
