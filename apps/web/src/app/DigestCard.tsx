import Link from "next/link";
import type { Digest } from "@/lib/api";
import { StageBadge } from "@/components/ui";
import type { EventKind } from "@/lib/api";
import styles from "./today.module.css";

const MODEL: Record<string, string> = { "claude-sonnet-5": "Written by Claude Sonnet 5", template: "Written by a template" };

function when(iso: string) {
  return new Date(iso).toLocaleString("en-IN", { timeZone: "Asia/Kolkata", weekday: "short", hour: "numeric", minute: "2-digit" });
}

/** The morning digest: a 100-word read, with the facts behind it one click away. */
export function DigestCard({ d }: { d: Digest }) {
  const t = d.trends;
  return (
    <section className={`ai-frame ${styles.panel} ${styles.digest}`} aria-labelledby="digest-h">
      <div className={styles.panelHead}>
        <h2 id="digest-h" className={styles.aiTitle}>
          <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 0c.4 3.9 2.1 5.6 6 6-3.9.4-5.6 2.1-6 6-.4-3.9-2.1-5.6-6-6 3.9-.4 5.6-2.1 6-6z" fill="#8b5cf6" /></svg>
          Morning digest
        </h2>
        <span className="faint">since {when(d.since)}</span>
      </div>
      <p className={styles.digestText}>{d.text}</p>
      <p className={`faint ${styles.byline}`}>{MODEL[d.model] ?? `Written by ${d.model}`}</p>

      <details className={styles.more}>
        <summary>What’s behind it</summary>
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
            <h3>Trends · this week</h3>
            <dl className={styles.stats}>
              <div><dt>Applied</dt><dd>{t.apps_this_week} <span className="faint">vs {t.apps_last_week}</span></dd></div>
              <div><dt>Replies</dt><dd>{t.responses_this_week}</dd></div>
              <div><dt>Rejections</dt><dd>{t.rejections_this_week}</dd></div>
              <div><dt>Reply rate, 30 days</dt><dd>{Math.round(t.response_rate_30d * 100)}%</dd></div>
            </dl>
          </div>
          <div>
            <h3>Next steps</h3>
            {d.next_steps.length === 0 ? <p className="faint">Nothing urgent.</p> : (
              <ul>{d.next_steps.map((s) => (
                <li key={s.id}><Link href={`/analysis?app=${s.id}`}><strong>{s.title}</strong> <span className="muted">· {s.company}</span></Link></li>
              ))}</ul>
            )}
          </div>
          <div>
            <h3>Suggested companies</h3>
            {d.suggested.length === 0 ? <p className="faint">Add preferred companies in Organizations to get suggestions.</p> : (
              <ul>{d.suggested.map((s) => (
                <li key={s.id}><Link href={`/organizations?org=${s.id}`}><strong>{s.name}</strong> <span className="muted">· {s.reason}</span></Link></li>
              ))}</ul>
            )}
          </div>
        </div>
      </details>
    </section>
  );
}
