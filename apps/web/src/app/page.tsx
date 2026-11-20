import Link from "next/link";
import { connection } from "next/server";
import { Suspense } from "react";
import { api, type Digest, type Suggestion } from "@/lib/api";
import { STAGES, STAGE_META, daysBetween, greeting, relativeDays } from "@/lib/format";
import { CompanyLogo, EmptyInbox, Offline, StageBadge } from "@/components/ui";
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

/** How many rows the two lists beside the chart show; the rest are in Pipeline. */
const TOP = 3;

async function Today() {
  await connection(); // greetings and "days quiet" use today's date: render per request, before any fetch
  let sum;
  let digest: Digest | null = null;
  let suggested: Suggestion[] | null = null;
  try {
    [sum, digest, suggested] = await Promise.all([api.summary(), api.digest().catch(() => null), api.suggestions().catch(() => null)]);
  } catch {
    return <Offline />;
  }
  const now = new Date();
  const name = process.env.DZ_DISPLAY_NAME;
  const nudge = sum.needs_nudge.slice(0, TOP);
  const recent = sum.recent_events.slice(0, TOP);
  const total = STAGES.reduce((n, s) => n + sum.by_stage[s], 0);

  return (
    <div className={styles.page}>
      <header className={styles.hero}>
        <h1 className="display">{greeting(now)}{name ? `, ${name}` : ""}</h1>
        <p className="eyebrow">{now.toLocaleDateString("en-IN", { weekday: "long", day: "numeric", month: "long" })}</p>
      </header>

      {sum.total === 0 ? (
        <EmptyInbox />
      ) : (
        <>
          {digest && <DigestCard d={digest} suggested={suggested} />}

          {/* The pipeline in one line: a proportional bar and its legend. */}
          <section aria-label="Pipeline at a glance" className={styles.pipeline}>
            <div className={styles.bar} aria-hidden="true">
              {STAGES.map((s) => sum.by_stage[s] > 0 && (
                <span key={s} data-stage={s} style={{ flexGrow: sum.by_stage[s] }} />
              ))}
            </div>
            <ul className={styles.legend}>
              {STAGES.map((s) => (
                <li key={s}>
                  <Link href={`/pipeline?stage=${s}`} data-stage={s}>
                    <i aria-hidden="true" />
                    <strong>{sum.by_stage[s]}</strong> {STAGE_META[s].label}
                  </Link>
                </li>
              ))}
              <li className={styles.legendTotal}>{total} in total</li>
            </ul>
          </section>

          {/* Left half: the two short lists; right half: the chart. Both end level. */}
          <div className={styles.split}>
            <div className={styles.stack}>
              <section className={`card ${styles.panel}`} aria-labelledby="nudge-h">
                <div className={styles.panelHead}>
                  <h2 id="nudge-h">Needs a nudge</h2>
                  {sum.needs_nudge.length > TOP && (
                    <Link href="/pipeline" className={styles.headLink}>+{sum.needs_nudge.length - TOP} more</Link>
                  )}
                </div>
                {nudge.length === 0 ? (
                  <p className={`muted ${styles.calm}`}>Everything has moved in the last two weeks.</p>
                ) : (
                  <ul className={styles.rows}>
                    {nudge.map((a) => (
                      <li key={a.id}>
                        <Link href={`/pipeline?app=${a.id}`} className={styles.row}>
                          <CompanyLogo company={a.company} stage={a.stage} />
                          <span className={styles.rowMain}>
                            <strong>{a.company}</strong>
                            <span className="faint">{a.role || "Role not detected"}</span>
                          </span>
                          <span className={styles.rowMeta}>{daysBetween(a.last_activity_at, now)} days quiet</span>
                        </Link>
                      </li>
                    ))}
                  </ul>
                )}
              </section>

              <section className={`card ${styles.panel}`} aria-labelledby="recent-h">
                <div className={styles.panelHead}>
                  <h2 id="recent-h">Latest movement</h2>
                  <Link href="/pipeline" className={styles.headLink}>All →</Link>
                </div>
                <ol className={styles.rows}>
                  {recent.map((e) => (
                    <li key={e.id}>
                      <Link href={`/pipeline?app=${e.application_id}`} className={styles.row}>
                        <CompanyLogo company={e.company} stage={e.kind} />
                        <span className={styles.rowMain}>
                          <strong>{e.company}</strong>
                          <span className="faint">{e.role || e.subject}</span>
                        </span>
                        <span className={styles.rowEnd}>
                          <StageBadge stage={e.kind} />
                          <span className={styles.rowMeta}>{relativeDays(e.occurred_at, now)}</span>
                        </span>
                      </Link>
                    </li>
                  ))}
                </ol>
              </section>
            </div>

            {digest && (
              <section className={`card ${styles.panel} ${styles.chartCard}`} aria-labelledby="trend-h">
                <div className={styles.panelHead}>
                  <h2 id="trend-h">Last 7 days</h2>
                  <span className={styles.rowMeta}>{digest.trends.waiting} waiting for a reply</span>
                </div>
                <div className={styles.chartFill}>
                  <TrendChart series={digest.series} fill />
                </div>
              </section>
            )}
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
      <div className="skeleton" style={{ height: 48, maxWidth: 360 }} />
      <div className="skeleton" style={{ height: 180 }} />
      <div className="skeleton" style={{ height: 40 }} />
      <div className={styles.split}>
        <div className="skeleton" style={{ height: 360 }} />
        <div className="skeleton" style={{ height: 360 }} />
      </div>
    </div>
  );
}
