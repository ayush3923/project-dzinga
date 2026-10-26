import Link from "next/link";
import { Suspense } from "react";
import { api, type Digest } from "@/lib/api";
import { STAGES, STAGE_META, brief, daysBetween, greeting, relativeDays } from "@/lib/format";
import { CompanyLogo, EmptyInbox, MailboxIcon, MailboxIcons, Offline, StageBadge } from "@/components/ui";
import styles from "./today.module.css";
import { DigestCard } from "./DigestCard";
import { TrendChart } from "./TrendChart";

export default function TodayPage() {
  return (
    <Suspense fallback={<TodaySkeleton />}>
      <Today />
    </Suspense>
  );
}

async function Today() {
  let sum;
  let digest: Digest | null = null;
  try {
    [sum, digest] = await Promise.all([api.summary(), api.digest().catch(() => null)]);
  } catch {
    return <Offline />;
  }
  const now = new Date();
  const name = process.env.DZ_DISPLAY_NAME;

  return (
    <div className={styles.page}>
      <header className={styles.hero}>
        <p className="eyebrow">
          {now.toLocaleDateString("en-IN", { weekday: "long", day: "numeric", month: "long" })}
        </p>
        <h1 className="display">
          {greeting(now)}
          {name ? `, ${name}` : ""}.
        </h1>
        {sum.total > 0 && <p className={styles.brief}>{brief(sum.by_stage, sum.needs_nudge.length)}</p>}
      </header>

      {sum.total === 0 ? (
        <EmptyInbox />
      ) : (
        <>
          {digest && (
            <div className={styles.grid}>
              <DigestCard d={digest} />
              <section className={`card ${styles.panel}`} aria-labelledby="trend-h">
                <div className={styles.panelHead}>
                  <h2 id="trend-h">Last 7 days</h2>
                  <span className="faint">{digest.trends.waiting} waiting now</span>
                </div>
                <TrendChart series={digest.series} />
              </section>
            </div>
          )}

          <section aria-label="Pipeline at a glance" className={styles.strip}>
            {STAGES.map((s) => (
              <Link key={s} href={`/pipeline?stage=${s}`} data-stage={s} className={styles.seg}
                style={{ flexGrow: Math.max(sum.by_stage[s], 0.35) }}>
                <span className={styles.segBar} />
                <span className={styles.segNum}>{sum.by_stage[s]}</span>
                <span className={styles.segLabel}>{STAGE_META[s].label}</span>
              </Link>
            ))}
          </section>

          <div className={styles.grid}>
            <section className={`card ${styles.panel}`} aria-labelledby="nudge-h">
              <div className={styles.panelHead}>
                <h2 id="nudge-h">Needs a nudge</h2>
                <span className="faint">quiet for 14+ days</span>
              </div>
              {sum.needs_nudge.length === 0 ? (
                <p className={`muted ${styles.calm}`}>Everything has moved recently. Nice.</p>
              ) : (
                <ul className={styles.list}>
                  {sum.needs_nudge.map((a) => (
                    <li key={a.id}>
                      <Link href={`/pipeline?app=${a.id}`} className={styles.row}>
                        <CompanyLogo company={a.company} stage={a.stage} />
                        <span className={styles.rowMain}>
                          <strong>{a.company}</strong>
                          <span className="muted">{a.role || "Role not detected"}</span>
                        </span>
                        <MailboxIcons accounts={a.accounts} />
                        <span className={styles.quiet}>{daysBetween(a.last_activity_at, now)}d</span>
                      </Link>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            <section className={`card ${styles.panel}`} aria-labelledby="recent-h">
              <div className={styles.panelHead}>
                <h2 id="recent-h">Latest movement</h2>
                <Link href="/pipeline" className="faint">
                  All applications →
                </Link>
              </div>
              <ol className={styles.timeline}>
                {sum.recent_events.map((e) => (
                  <li key={e.id} data-stage={e.kind}>
                    <Link href={`/pipeline?app=${e.application_id}`} className={styles.tlItem}>
                      <span className={styles.tlDot} />
                      <span className={styles.rowMain}>
                        <span>
                          <strong>{e.company}</strong> <span className="muted">· {STAGE_META[e.kind].verb}</span>
                        </span>
                        <span className="faint">{e.role || e.subject}</span>
                      </span>
                      <MailboxIcon account={e.account} />
                      <span className="faint">{relativeDays(e.occurred_at, now)}</span>
                    </Link>
                  </li>
                ))}
              </ol>
            </section>
          </div>

          {sum.by_stage.offer > 0 && (
            <p className={styles.footnote}>
              <StageBadge stage="offer" /> You have an offer on the table. Open it from the pipeline to see the full thread.
            </p>
          )}
        </>
      )}
    </div>
  );
}

function TodaySkeleton() {
  return (
    <div className={styles.page} aria-busy="true">
      <div className="skeleton" style={{ height: 120, maxWidth: 560 }} />
      <div className="skeleton" style={{ height: 72 }} />
      <div className={styles.grid}>
        <div className="skeleton" style={{ height: 280 }} />
        <div className="skeleton" style={{ height: 280 }} />
      </div>
    </div>
  );
}
