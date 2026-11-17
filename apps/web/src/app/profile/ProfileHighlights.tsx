import Link from "next/link";
import type { CSSProperties } from "react";
import type { Experience, Mailbox, NewsItem, Profile, QuizStats } from "@/lib/api";
import { MailboxIcon } from "@/components/ui";
import { PhotoControl } from "./PhotoControl";
import styles from "./profile.module.css";

/** Each card shows at most this many pieces of information; the rest is a click away. */
const MAX = 5;

/** Corner link to a card's detail. */
function More({ href, label }: { href: string; label: string }) {
  return (
    <Link href={href} className={styles.more} aria-label={label} title={label}>
      <span aria-hidden="true">↗</span>
    </Link>
  );
}

/** Photo, name (bold), role, and three facts: experience, skills and location. */
export function ProfileHero({ p }: { p: Profile }) {
  const role = [p.current_title, p.current_company].filter(Boolean).join(" · ");
  return (
    <section className={`card ${styles.hero}`} aria-label="You">
      <More href="#edit-profile" label="Edit profile" />
      <PhotoControl name={p.full_name} photoAt={p.photo_updated_at ?? null} />
      <div className={styles.who}>
        <p className={styles.name}><strong>{p.full_name || "Add your name"}</strong></p>
        {role && <p className={styles.role}>{role}</p>}
      </div>
      <dl className={styles.heroStats}>
        <div><dt>Experience</dt><dd>{p.total_years ? `${p.total_years} yrs` : "—"}</dd></div>
        <div><dt>Skills</dt><dd>{p.skills.length || "—"}</dd></div>
        <div><dt>Based in</dt><dd title={p.location}>{p.location.split(",")[0] || "—"}</dd></div>
      </dl>
    </section>
  );
}

const score = (v: number | null) => (v === null ? "—" : `${v}/10`);

function dayLabel(day: string) {
  return new Date(`${day}T00:00:00+05:30`).toLocaleDateString("en-IN", { day: "numeric", month: "short", timeZone: "Asia/Kolkata" });
}

/** Quiz performance, highlighted: 7-day accuracy, streak, written and spoken scores, and the daily chart. */
export function QuizPerformance({ s }: { s: QuizStats | null }) {
  const head = (
    <div className={styles.cardTop}>
      <h2 id="perf-h">Quiz performance</h2>
      <More href="/quiz" label="Open today’s quiz" />
    </div>
  );
  if (!s || s.quizzes_taken === 0) {
    return (
      <section className={`card ${styles.perf}`} aria-labelledby="perf-h">
        {head}
        <p className="muted">
          {s ? "Answer today’s quiz to start your scores." : <>The quiz service isn&rsquo;t reachable. Start it with <code>make quiz</code>.</>}
        </p>
      </section>
    );
  }
  const days = [...s.days].reverse();
  return (
    <section className={`card ${styles.perf}`} aria-labelledby="perf-h">
      {head}
      <div className={styles.perfBody}>
        <dl className={styles.kpis}>
          <div className={styles.kpiMain}>
            <dt>Accuracy, last 7 days</dt>
            <dd>{s.accuracy_7d === null ? "—" : `${s.accuracy_7d}%`}</dd>
          </div>
          <div><dt>Streak</dt><dd>{s.streak} {s.streak === 1 ? "day" : "days"}</dd></div>
          <div><dt>Written</dt><dd>{score(s.written_avg)}</dd></div>
          <div><dt>Spoken</dt><dd>{score(s.spoken_avg)}</dd></div>
        </dl>
        <figure className={styles.chart}>
          <figcaption className="faint">Multiple-choice right per day, of 5</figcaption>
          <ol className={styles.bars} aria-label="Multiple-choice questions right per day, last 14 quizzes">
            {days.map((d) => (
              <li key={d.day} title={`${dayLabel(d.day)}: ${d.correct} of 5 right`}>
                <span className={styles.barVal}>{d.correct}</span>
                <span className={styles.bar} style={{ height: `${Math.max(4, (d.correct / 5) * 100)}%` }} />
                <span className={styles.barDay}>{dayLabel(d.day)}</span>
              </li>
            ))}
          </ol>
        </figure>
      </div>
    </section>
  );
}

/** Up to five key news items, gathered with each day's quiz. It flows slowly; hover or focus pauses it. */
export function KeyNews({ news }: { news: { day: string; items: NewsItem[] } | null }) {
  const items = (news?.items ?? []).slice(0, MAX);
  const flow = items.length > 3;
  const item = (x: NewsItem, i: number, hidden?: boolean) => (
    <li key={`${hidden ? "b" : "a"}${i}`} className={styles.newsItem} aria-hidden={hidden || undefined}>
      <span className={styles.tag}>{x.topic === "ai_news" ? "AI" : x.company}</span>
      <a href={x.url} target="_blank" rel="noopener noreferrer" tabIndex={hidden ? -1 : undefined}>
        <strong>{x.title}</strong>
      </a>
      <span className={styles.summary}>{x.summary}</span>
      <span className="faint">{x.source_title}{x.published_on && ` · ${dayLabel(x.published_on)}`}</span>
    </li>
  );
  return (
    <section className={`card ${styles.tile}`} aria-labelledby="news-h">
      <div className={styles.cardTop}>
        <h2 id="news-h">Key news</h2>
        {news?.day && <span className="faint">{dayLabel(news.day)}</span>}
      </div>
      {items.length === 0 ? (
        <p className="muted">News arrives with the next daily quiz.</p>
      ) : (
        <div className={styles.feed} tabIndex={0} aria-label="Key news feed">
          <ul className={styles.feedTrack} style={flow ? { animationDuration: `${items.length * 8}s` } : { animation: "none" }}>
            {items.map((x, i) => item(x, i))}
            {flow && items.map((x, i) => item(x, i, true))}
          </ul>
        </div>
      )}
    </section>
  );
}

function months(e: Experience, now: Date) {
  const [y, m] = e.start_month.split("-").map(Number);
  if (!y || !m) return null;
  const [ey, em] = e.end_month ? e.end_month.split("-").map(Number) : [now.getFullYear(), now.getMonth() + 1];
  return Math.max(1, (ey - y) * 12 + (em - m) + 1);
}

function span(n: number | null) {
  if (n === null) return "—";
  const y = Math.floor(n / 12), m = n % 12;
  return [y && `${y}y`, m && `${m}m`].filter(Boolean).join(" ") || "—";
}

/** The five most recent roles, with how long each lasted. */
export function CareerHistory({ experiences, now }: { experiences: Experience[]; now: Date }) {
  const rows = experiences.filter((e) => e.title || e.company).slice(0, MAX);
  return (
    <section className={`card ${styles.tile}`} aria-labelledby="career-h">
      <div className={styles.cardTop}>
        <h2 id="career-h">Career history</h2>
        <More href="#edit-profile" label="Edit experience" />
      </div>
      {rows.length === 0 ? (
        <p className="muted">Add your roles in Edit profile below.</p>
      ) : (
        <table className={styles.table}>
          <thead><tr><th scope="col">Role</th><th scope="col" className={styles.num}>Duration</th></tr></thead>
          <tbody>
            {rows.map((e, i) => (
              <tr key={i}>
                <td>
                  <span className={styles.mbName}>
                    <span><strong title={e.title}>{e.title}</strong>{!e.end_month && <span className={styles.now}>Now</span>}</span>
                    <span className={styles.status} title={e.company}>{e.company}</span>
                  </span>
                </td>
                <td className={styles.num}>{span(months(e, now))}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

/** Up to five mailboxes: status and how many job emails each has brought in. */
export function MailSummary({ mailboxes }: { mailboxes: Mailbox[] }) {
  const rows = mailboxes.slice(0, MAX);
  const state = (m: Mailbox) =>
    !m.credential_saved ? { c: "var(--ink-3)", t: "Not connected" }
      : m.last_status === "error" ? { c: "var(--nrds-critical)", t: "Last sync failed" }
        : !m.enabled ? { c: "var(--ink-3)", t: "Paused" }
          : { c: "var(--nrds-success)", t: "Connected" };
  return (
    <section className={`card ${styles.tile}`} aria-labelledby="mail-h">
      <div className={styles.cardTop}>
        <h2 id="mail-h">Mailboxes</h2>
        <More href="#mailboxes" label="Manage mailboxes" />
      </div>
      {rows.length === 0 ? (
        <p className="muted">No mailboxes yet. Add one in Manage mailboxes below.</p>
      ) : (
        <table className={styles.table}>
          <thead><tr><th scope="col">Mailbox</th><th scope="col" className={styles.num}>Job emails</th></tr></thead>
          <tbody>
            {rows.map((m) => {
              const s = state(m);
              return (
                <tr key={m.id}>
                  <td>
                    <span className={styles.mbCell}>
                      <MailboxIcon account={m.address} />
                      <span className={styles.mbName}>
                        <span title={m.address}>{m.address}</span>
                        <span className={styles.status}><span className="dot" style={{ "--c": s.c } as CSSProperties} />{s.t}</span>
                      </span>
                    </span>
                  </td>
                  <td className={styles.num}><strong>{m.emails}</strong></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </section>
  );
}
