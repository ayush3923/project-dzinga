import Link from "next/link";
import { api, type Briefing, type Figure, type OrgDetail } from "@/lib/api";
import { shortDate } from "@/lib/format";
import { CompanyLogo, MailboxIcons, StageBadge } from "@/components/ui";
import { DiagnoseButton } from "./DiagnoseButton";
import { ResearchButton } from "@/components/ResearchButton";
import { MarkSeen } from "./MarkSeen";
import styles from "./organizations.module.css";

const SOURCE = { glassdoor: "Glassdoor (employees)", gartner_peer_insights: "Gartner Peer Insights (customers)" } as const;

export async function OrgDrawer({ id, closeHref }: { id: number; closeHref: string }) {
  let o: OrgDetail;
  try {
    o = await api.organization(id);
  } catch {
    return null;
  }
  const close = closeHref;
  const unseen = o.briefing_unseen;
  const p = o.profile;

  return (
    <>
      <Link href={close} scroll={false} className={styles.scrim} aria-label="Close details" />
      <aside className={styles.drawer} aria-labelledby="org-title">
        <div className={styles.drawerHead}>
          <CompanyLogo company={o.name} />
          <div>
            <h2 id="org-title" className="display">{o.name}</h2>
            <p className="muted">
              {o.applications} {o.applications === 1 ? "application" : "applications"}
              {o.first_applied_at && <> · first on {shortDate(o.first_applied_at)}</>}
              {o.avg_response_days !== null ? <> · replies in {o.avg_response_days} d on average</> : <> · no replies yet</>}
            </p>
          </div>
          <Link href={close} scroll={false} className={styles.close} aria-label="Close">✕</Link>
        </div>

        {o.briefing && <BriefingSection b={o.briefing} unseen={unseen} newsCount={o.news_count} />}
        {(unseen || o.diagnosis_unseen) && <MarkSeen id={o.id} />}

        {/* Lifetime view: every application to this organization */}
        <section className={styles.section}>
          <h3>Applications</h3>
          {o.application_history.length === 0 ? (
            <p className="muted">You haven’t applied here yet.</p>
          ) : (
            <ol className={styles.lifetime}>
              {o.application_history.map((a) => (
                <li key={a.id}>
                  <span className={styles.ltDate}>{shortDate(a.timeline[0]?.at ?? a.applied_at)}</span>
                  <span className={styles.ltMain}>
                    <Link href={`/analysis?view=all&app=${a.id}`} className={styles.ltRole}>{a.role || "Role not detected"}</Link>
                    <span className="faint">
                      {a.timeline.map((s) => s.label).join(" → ")}
                      {a.days_to_outcome !== null && ` · ${a.days_to_outcome} d to outcome`}
                    </span>
                  </span>
                  <span className={styles.ltStatus}>
                    <StageBadge stage={a.stage} />
                    <MailboxIcons accounts={a.accounts} />
                  </span>
                </li>
              ))}
            </ol>
          )}
        </section>

        {/* Diagnosis, written by Claude in the app: the daily run keeps five queued at random, or on request. All are kept. */}
        {(o.diagnosis_suggested || o.diagnosis || o.diagnosis_requested_at || o.applications > 0) && (
          <section className={styles.section}>
            <h3>
              Diagnosis
              {o.diagnosis_unseen && <span className={styles.newTag}>New</span>}
              {o.diagnosis_reason && <span className="faint"> · {o.diagnosis_reason}</span>}
            </h3>
            {o.diagnosis ? (
              <>
                <p>{o.diagnosis.summary}</p>
                <ul className={styles.causes}>
                  {o.diagnosis.causes.map((c, i) => (
                    <li key={i} data-confidence={c.confidence}>
                      <span className={styles.conf}>{c.confidence}</span>
                      <span>{c.text}</span>
                    </li>
                  ))}
                </ul>
                {o.diagnosis.actions.length > 0 && (
                  <>
                    <p className={styles.listTitle}>What to do</p>
                    <ul className={styles.list}>{o.diagnosis.actions.map((a, i) => <li key={i}>{a}</li>)}</ul>
                  </>
                )}
                <p className="faint">
                  {shortDate(o.diagnosis.created_at)} · {o.diagnosis.model}.{" "}
                  <DiagnoseButton id={o.id} label="Ask again" requestedAt={o.diagnosis_requested_at} quiet />
                </p>
                {o.earlier_diagnoses.length > 0 && (
                  <details className={styles.more}>
                    <summary>Earlier diagnoses ({o.earlier_diagnoses.length})</summary>
                    <ol className={styles.earlier}>
                      {o.earlier_diagnoses.map((d) => (
                        <li key={d.created_at}>
                          <span className="faint">{shortDate(d.created_at)}</span>
                          <span>{d.summary}</span>
                        </li>
                      ))}
                    </ol>
                  </details>
                )}
              </>
            ) : (
              <>
                <p className="muted">Claude reads your history with {o.name} (timing, stages, fit scores, rejections, how many roles) and explains what&rsquo;s likely going on and what to do next. Each morning&rsquo;s run keeps five organizations queued for a diagnosis, picked at random.</p>
                <DiagnoseButton id={o.id} label="Diagnose" requestedAt={o.diagnosis_requested_at} />
              </>
            )}
          </section>
        )}

        {/* Company profile (researched by Claude in the app, then saved here) */}
        <section className={styles.section}>
          <h3>Company profile</h3>
          {!p ? (
            <>
              <p className="muted">
                Overview, an interview angle based on your intent, key news from the last 3 months, products, leadership,
                and employee and customer ratings. Researched on the web by Claude in the Claude app, at no API cost.
              </p>
              <ResearchButton kind="organizations" id={o.id} label="Queue for Claude" requestedAt={o.research_requested_at} />
            </>
          ) : (
            <>
              <p>{p.overview}</p>
              <div className={styles.angle}>
                <strong>Interview angle</strong>
                <p>{p.interview_angle}</p>
              </div>

              <div className={styles.ratings}>
                {p.ratings.map((r) => (
                  <div key={r.source} className={styles.rating}>
                    <span className="faint">{SOURCE[r.source]}</span>
                    {r.score !== null ? (
                      <strong>{r.score.toFixed(1)}<small>/{r.scale}</small></strong>
                    ) : (
                      <strong className="faint">not found</strong>
                    )}
                    <span className="faint">
                      {r.review_count !== null && `${r.review_count.toLocaleString("en-IN")} reviews · `}
                      {r.url ? <a href={r.url} target="_blank" rel="noopener noreferrer" className={styles.link}>source</a> : "no source"}
                      {r.as_of && ` · checked ${shortDate(r.as_of)}`}
                    </span>
                  </div>
                ))}
              </div>

              <details className={styles.more} open>
                <summary>Key news ({p.news.length})</summary>
                <ul className={styles.news}>
                  {p.news.map((n, i) => (
                    <li key={i}>
                      <span className="faint">{n.published_on ? shortDate(n.published_on) : "undated"}</span>
                      <span>
                        {n.url ? <a href={n.url} target="_blank" rel="noopener noreferrer" className={styles.link}>{n.title}</a> : <strong>{n.title}</strong>}
                        {n.summary && <span className="muted"> · {n.summary}</span>}
                      </span>
                    </li>
                  ))}
                  {p.news.length === 0 && <li className="faint">No significant news found in the last 3 months.</li>}
                </ul>
              </details>
              <details className={styles.more}>
                <summary>Key products ({p.products.length})</summary>
                <ul className={styles.list}>{p.products.map((x, i) => <li key={i}><strong>{x.name}</strong> <span className="muted">{x.description}</span></li>)}</ul>
              </details>
              <details className={styles.more}>
                <summary>Leadership ({p.leaders.length})</summary>
                <ul className={styles.list}>{p.leaders.map((x, i) => <li key={i}><strong>{x.name}</strong> <span className="muted">{x.title}</span></li>)}</ul>
              </details>
              <p className="faint">
                Researched {shortDate(p.created_at)} · {p.model}
                {o.profile_versions > 1 && <> · version {o.profile_versions}, earlier ones kept</>}. Verify facts before relying on them.{" "}
                <ResearchButton kind="organizations" id={o.id} label="Refresh" requestedAt={o.research_requested_at} quiet />
              </p>
            </>
          )}
        </section>
      </aside>
    </>
  );
}

/** Leaders, revenue and news Claude found for this company while building a daily quiz. */
function BriefingSection({ b, unseen, newsCount }: { b: Briefing; unseen: boolean; newsCount: number }) {
  const facts: { label: string; value: string; detail?: string; url?: string }[] = [];
  if (b.ceo) facts.push({ label: "CEO", value: b.ceo, url: b.leaders_url || undefined });
  if (b.cpo) facts.push({ label: "Product head", value: b.cpo, url: b.leaders_url || undefined });
  const fig = (label: string, f: Figure) => f.value && facts.push({ label, value: f.value, detail: f.period, url: f.url });
  fig("Last full year’s revenue", b.revenue_year);
  fig("Last reported quarter", b.revenue_quarter);
  return (
    <section className={styles.section} aria-labelledby="brief-h">
      <h3 id="brief-h">
        From your daily quiz {unseen && <span className={styles.newTag}>New</span>}
      </h3>
      {facts.length > 0 && (
        <dl className={styles.facts}>
          {facts.map((f) => (
            <div key={f.label}>
              <dt>{f.label}</dt>
              <dd>
                <strong>{f.value}</strong>
                {(f.detail || f.url) && (
                  <span className="faint">
                    {f.detail}
                    {f.url && <>{f.detail && " · "}<a href={f.url} target="_blank" rel="noopener noreferrer" className={styles.link}>source</a></>}
                  </span>
                )}
              </dd>
            </div>
          ))}
        </dl>
      )}
      {b.news.length > 0 && (
        <ul className={styles.news}>
          {b.news.map((n) => (
            <li key={n.url}>
              <span className="faint">{n.published_on ? shortDate(n.published_on) : `found ${shortDate(n.day)}`}</span>
              <span>
                <a href={n.url} target="_blank" rel="noopener noreferrer" className={styles.link}>{n.title}</a>
                {n.summary && <span className="muted"> · {n.summary}</span>}
                {n.source_title && <span className="faint"> · {n.source_title}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
      <p className="faint">
        Updated {shortDate(b.updated_at)} · {b.model}
        {newsCount > b.news.length && <> · newest {b.news.length} of {newsCount} news items</>}. Figures are as published; check the source before quoting them.
      </p>
    </section>
  );
}
