import Link from "next/link";
import { Suspense } from "react";
import { api, type AppDetail, type Application } from "@/lib/api";
import { STAGES, STAGE_META, isStage, relativeDays, roundsLabel, shortDate, type StageKey } from "@/lib/format";
import { CompanyLogo, EmptyInbox, MailboxIcon, MailboxIcons, Offline, StageBadge } from "@/components/ui";
import { RoundsControl } from "./RoundsControl";
import { StageControl } from "./StageControl";
import { EditDetails } from "@/components/EditDetails";
import { FitPill } from "@/app/analysis/FitPill";
import { FitRequestButton } from "./FitRequestButton";
import { InterviewNoteForm } from "./InterviewNoteForm";
import styles from "./pipeline.module.css";

type Params = { stage?: StageKey; q?: string; app?: string };
const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default function PipelinePage({ searchParams }: PageProps<"/pipeline">) {
  return (
    <div className={styles.page}>
      <header className={styles.head}>
        <div>
          <p className="eyebrow">Stage 5 · Follow-ups</p>
          <h1 className="display">Pipeline</h1>
        </div>
      </header>
      <Suspense fallback={<div className="skeleton" style={{ height: 420 }} />}>
        <Board searchParams={searchParams} />
      </Suspense>
    </div>
  );
}

function hrefWith(p: Params, patch: Partial<Params>) {
  const next = { ...p, ...patch };
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(next)) if (v) qs.set(k, v);
  const s = qs.toString();
  return `/pipeline${s ? `?${s}` : ""}`;
}

async function Board({ searchParams }: { searchParams: PageProps<"/pipeline">["searchParams"] }) {
  const sp = await searchParams;
  const p: Params = {
    stage: (() => { const s = one(sp.stage); return isStage(s) ? s : undefined; })(),
    q: one(sp.q)?.slice(0, 200) || undefined,
    app: /^\d+$/.test(one(sp.app) ?? "") ? one(sp.app) : undefined,
  };

  let apps: Application[];
  try {
    apps = (await api.applications({ stage: p.stage, q: p.q })).applications;
  } catch {
    return <Offline />;
  }
  const now = new Date();
  if (apps.length === 0 && !p.q && !p.stage) return <EmptyInbox />;

  const columns = p.stage ? [p.stage] : STAGES.filter((s) => s !== "rejected");
  const closed = p.stage ? [] : apps.filter((a) => a.stage === "rejected");

  return (
    <>
      <div className={styles.toolbar}>
        <form action="/pipeline" className={styles.search} role="search">
          {p.stage && <input type="hidden" name="stage" value={p.stage} />}
          <label htmlFor="q" className="visually-hidden">Search applications</label>
          <input id="q" name="q" className="input" defaultValue={p.q} placeholder="Search company, role or email…" autoComplete="off" />
        </form>
        <nav className={styles.chips} aria-label="Filter by stage">
          <Link href={hrefWith(p, { stage: undefined, app: undefined })} className="chip" aria-current={!p.stage}>
            All <span className="faint">{p.stage ? "" : apps.length}</span>
          </Link>
          {STAGES.map((s) => (
            <Link key={s} href={hrefWith(p, { stage: s, app: undefined })} className="chip" data-stage={s}
              aria-current={p.stage === s}>
              <span className="dot" />
              {STAGE_META[s].label}
            </Link>
          ))}
        </nav>
      </div>

      {apps.length === 0 ? (
        <p className="muted">No applications match that search.</p>
      ) : (
        <div className={styles.board} data-single={!!p.stage}>
          {columns.map((s) => {
            const items = apps.filter((a) => a.stage === s);
            return (
              <section key={s} className={styles.col} data-stage={s} aria-label={STAGE_META[s].label}>
                <h2 className={styles.colHead}>
                  <span className="dot" />
                  {STAGE_META[s].label}
                  <span className="faint">{items.length}</span>
                </h2>
                {items.map((a) => (
                  <AppCard key={a.id} a={a} href={hrefWith(p, { app: String(a.id) })} now={now}
                    selected={p.app === String(a.id)} />
                ))}
                {items.length === 0 && <p className={styles.emptyCol}>Nothing here</p>}
              </section>
            );
          })}
        </div>
      )}

      {closed.length > 0 && (
        <details className={styles.closed}>
          <summary>
            <span className="dot" data-stage="rejected" /> {closed.length} closed{" "}
            {closed.length === 1 ? "application" : "applications"}
          </summary>
          <div className={styles.closedGrid}>
            {closed.map((a) => (
              <AppCard key={a.id} a={a} href={hrefWith(p, { app: String(a.id) })} now={now} />
            ))}
          </div>
        </details>
      )}

      {p.app && <Drawer id={Number(p.app)} closeHref={hrefWith(p, { app: undefined })} />}
    </>
  );
}

function AppCard({ a, href, now, selected }: { a: Application; href: string; now: Date; selected?: boolean }) {
  const rounds = roundsLabel(a.rounds_done, a.next_step, a.stage);
  return (
    <Link href={href} scroll={false} className={styles.appCard} data-stage={a.stage} aria-current={selected}>
      <div className={styles.cardTop}>
        <CompanyLogo company={a.company} stage={a.stage} />
        <div className={styles.cardTitle}>
          <strong>{a.company}</strong>
          <span className="muted">{a.role || "Role not detected"}</span>
        </div>
      </div>
      {rounds && <p className={styles.rounds}>{rounds}</p>}
      <div className={styles.cardMeta}>
        <span className={styles.metaLeft}>
          <MailboxIcons accounts={a.accounts} />
          Applied {shortDate(a.applied_at)}
        </span>
        <span>{relativeDays(a.last_activity_at, now)}</span>
      </div>
    </Link>
  );
}

/** The fit score in brief, linking to the full breakdown on the Analysis page. */
function FitSummary({ id, detail }: { id: number; detail: AppDetail | null }) {
  if (!detail) return null;
  const f = detail.fit;
  if (!f) {
    return (
      <section className={styles.fitBox} aria-label="Fit score">
        <span className="faint">No fit score yet.</span>
        {detail.fit_requested_at
          ? <span className="faint">Queued for Claude {shortDate(detail.fit_requested_at)}; it&rsquo;s done on the next scheduled run.</span>
          : <FitRequestButton id={id} />}
      </section>
    );
  }
  return (
    <section className={styles.fitBox} aria-label="Fit score">
      <div className={styles.fitHead}>
        <FitPill total={f.total} band={f.band} />
        <span className="faint">Fit score · {f.model.startsWith("claude-opus") ? "Claude Opus" : f.model === "rules" ? "rule-based" : f.model} · {shortDate(f.created_at)}</span>
      </div>
      {f.verdict && <p className={styles.fitVerdict}>{f.verdict}</p>}
      <Link href={`/analysis?view=all&app=${id}`} className={styles.orgLink}>See the full fit analysis →</Link>
    </section>
  );
}

const CHANCE = { high: "Good chance", medium: "Even chance", low: "Long shot" } as const;

/** The user's notes on interview rounds and Claude's read on the chances of selection. */
function InterviewSection({ app: a, detail }: { app: Application; detail: AppDetail | null }) {
  if (!detail || (a.stage !== "interview" && detail.interview_notes.length === 0)) return null;
  const r = detail.interview_review;
  const lastNote = detail.interview_notes.at(-1);
  // A review is stale when notes were added after it.
  const stale = r && lastNote && Date.parse(lastNote.created_at) > Date.parse(r.created_at);
  return (
    <section className={styles.interviewBox} aria-labelledby="iv-h">
      <h3 id="iv-h">Interview notes</h3>
      {r && (
        <div className={styles.review} data-chance={r.chance}>
          <p className={styles.reviewHead}>
            <strong>{CHANCE[r.chance]}</strong>
            <span className="faint">Claude’s read · {shortDate(r.created_at)}{stale && " · before your latest notes"}</span>
          </p>
          <p>{r.summary}</p>
          {r.signals.length > 0 && <><p className={styles.reviewTitle}>Working for you</p><ul>{r.signals.map((x, i) => <li key={i}>{x}</li>)}</ul></>}
          {r.concerns.length > 0 && <><p className={styles.reviewTitle}>Watch out for</p><ul>{r.concerns.map((x, i) => <li key={i}>{x}</li>)}</ul></>}
          {r.next_steps.length > 0 && <><p className={styles.reviewTitle}>Before the next round</p><ul>{r.next_steps.map((x, i) => <li key={i}>{x}</li>)}</ul></>}
        </div>
      )}
      {detail.review_requested_at && (
        <p className="faint">Queued for Claude {shortDate(detail.review_requested_at)}; the review arrives on its next run.</p>
      )}
      {detail.interview_notes.length > 0 && (
        <ol className={styles.notes}>
          {[...detail.interview_notes].reverse().map((n) => (
            <li key={n.id}>
              <span className="faint">{n.round > 0 ? `Round ${n.round}` : "Notes"} · {shortDate(n.created_at)}</span>
              <p>{n.body}</p>
            </li>
          ))}
        </ol>
      )}
      {a.stage === "interview" && <InterviewNoteForm id={a.id} round={Math.max(a.rounds_done, 1)} />}
    </section>
  );
}

async function Drawer({ id, closeHref }: { id: number; closeHref: string }) {
  let data;
  try {
    data = await api.application(id);
  } catch {
    return null;
  }
  const detail = await api.detail(id).catch(() => null);
  const { application: a, events } = data;
  return (
    <>
      <Link href={closeHref} scroll={false} className={styles.scrim} aria-label="Close details" />
      <aside className={styles.drawer} aria-labelledby="drawer-title">
        <div className={styles.drawerHead}>
          <CompanyLogo company={a.company} stage={a.stage} />
          <div>
            <h2 id="drawer-title" className="display">{a.company}</h2>
            <p className="muted">{a.role || "Role not detected"}</p>
          </div>
          <Link href={closeHref} scroll={false} className={styles.close} aria-label="Close">✕</Link>
        </div>

        <dl className={styles.facts}>
          <div><dt>Stage</dt><dd><StageBadge stage={a.stage} /></dd></div>
          <div><dt>Applied</dt><dd>{shortDate(a.applied_at)}</dd></div>
          <div><dt>Last activity</dt><dd>{shortDate(a.last_activity_at)}</dd></div>
          <div>
            <dt>{a.accounts.length > 1 ? "Mailboxes" : "Mailbox"}</dt>
            <dd className={styles.mailList}>
              {a.accounts.map((acc) => (
                <span key={acc}><MailboxIcon account={acc} /> {acc}</span>
              ))}
            </dd>
          </div>
        </dl>

        <FitSummary id={a.id} detail={detail} />

        <EditDetails id={a.id} company={a.company} role={a.role} jobUrl={a.job_url} basePath="/pipeline?app=" />
        <StageControl id={a.id} stage={a.stage} manual={a.stage_is_manual} />
        <RoundsControl id={a.id} stage={a.stage} rounds={a.rounds_done} next={a.next_step} />
        <InterviewSection app={a} detail={detail} />
        {a.company && (
          <Link href={`/organizations?company=${encodeURIComponent(a.company)}`} className={styles.orgLink}>
            All applications to {a.company} →
          </Link>
        )}

        <h3 className={styles.tlHead}>Timeline <span className="faint">{events.length} emails</span></h3>
        <ol className={styles.tl}>
          {events.map((e) => (
            <li key={e.id} data-stage={e.kind}>
              <span className={styles.tlDot} />
              <details>
                <summary>
                  <span className={styles.tlTitle}>{e.subject || "(no subject)"}</span>
                  <span className={`faint ${styles.metaLeft}`}>
                    <MailboxIcon account={e.account} />
                    {STAGE_META[e.kind].verb} · {shortDate(e.occurred_at)}
                  </span>
                </summary>
                <p className={styles.snippet}>{e.snippet}</p>
                <p className="faint">From {e.sender}</p>
              </details>
            </li>
          ))}
        </ol>
      </aside>
    </>
  );
}
