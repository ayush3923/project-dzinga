import Link from "next/link";
import { connection } from "next/server";
import { Suspense } from "react";
import { api, type AnalysisReport, type AppAnalysis, type DurationStat } from "@/lib/api";
import { STAGE_META, companyKey } from "@/lib/format";
import { FIRST_DIR, SORT_LABELS, isSortKey, sortApps, type SortDir, type SortKey } from "@/lib/sort";
import { CompanyLogo, EmptyInbox, MailboxIcons, Offline, StageBadge } from "@/components/ui";
import { AnalysisDrawer } from "./AnalysisDrawer";
import { FitPill } from "./FitPill";
import styles from "./analysis.module.css";

type View = "open" | "closed" | "all";
const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default function AnalysisPage({ searchParams }: PageProps<"/analysis">) {
  return (
    <div className={styles.page}>
      <header className={styles.head}>
        <p className="eyebrow">Application analysis</p>
        <h1 className="display">How your search is going</h1>
      </header>
      <Suspense fallback={<div className="skeleton" style={{ height: 480 }} />}>
        <Report searchParams={searchParams} />
      </Suspense>
    </div>
  );
}

async function Report({ searchParams }: { searchParams: PageProps<"/analysis">["searchParams"] }) {
  await connection(); // sorting and "days" use the current time: render per request, before any fetch
  const sp = await searchParams;
  const v = one(sp.view);
  const view: View = v === "closed" || v === "all" ? v : "open";
  const appId = /^\d+$/.test(one(sp.app) ?? "") ? Number(one(sp.app)) : undefined;
  const s = one(sp.sort);
  const sortKey: SortKey | undefined = isSortKey(s) ? s : undefined;
  const dir: SortDir = one(sp.dir) === "asc" || one(sp.dir) === "desc" ? (one(sp.dir) as SortDir) : sortKey ? FIRST_DIR[sortKey] : "asc";

  let rep: AnalysisReport;
  try {
    rep = await api.analysis();
  } catch {
    return <Offline />;
  }
  if (rep.applications.length === 0) return <EmptyInbox />;

  const open = rep.applications.filter((a) => a.open);
  const closed = rep.applications.filter((a) => !a.open);
  const list = view === "open" ? open : view === "closed" ? closed : rep.applications;
  // Default order: what needs attention first (open) or most recent (closed).
  const sorted = sortKey ? sortApps(list, sortKey, dir) : [...list].sort((a, b) =>
    view === "open"
      ? Number(b.next_move.urgent) - Number(a.next_move.urgent) || b.days_in_stage - a.days_in_stage
      : Date.parse(b.stage_since) - Date.parse(a.stage_since),
  );
  const href = (patch: { view?: View; app?: number; sort?: SortKey | null; dir?: SortDir }) => {
    const q = new URLSearchParams({ view: patch.view ?? view });
    const k = patch.sort === null ? undefined : patch.sort ?? sortKey;
    if (k) {
      q.set("sort", k);
      q.set("dir", patch.sort ? patch.dir ?? FIRST_DIR[k] : dir);
    }
    if (patch.app) q.set("app", String(patch.app));
    return `/analysis?${q}`;
  };
  // Clicking the active column flips its direction; another column starts in its natural direction.
  const sortHref = (k: SortKey) => href({ sort: k, dir: k === sortKey ? (dir === "asc" ? "desc" : "asc") : FIRST_DIR[k] });
  const sortLink = (k: SortKey) => {
    const active = k === sortKey;
    const next = active && dir === "asc" ? "descending" : active ? "ascending" : FIRST_DIR[k] === "asc" ? "ascending" : "descending";
    return (
      <Link href={sortHref(k)} scroll={false} className={styles.sortLink} aria-current={active ? "true" : undefined}
        aria-label={`Sort by ${SORT_LABELS[k].toLowerCase()}, ${next}`}>
        {SORT_LABELS[k]}
        <span aria-hidden="true" className={styles.sortArrow}>{active ? (dir === "asc" ? "↑" : "↓") : "↕"}</span>
      </Link>
    );
  };
  const firstResponse = rep.outcomes.find((o) => o.key === "first_response");
  const selected = appId ? rep.applications.find((a) => a.id === appId) : undefined;

  return (
    <>
      <p className={styles.lead}>
        {rep.funnel.applied} applications · {rep.funnel.response_rate}% got a reply
        {firstResponse && firstResponse.n > 0 && <> · first reply after {firstResponse.avg_days.toFixed(1)} days on average</>}
      </p>

      <section className={styles.tiles} aria-label="Time to outcome">
        {rep.outcomes.map((o) => (
          <div key={o.key} className={`card ${styles.tile}`}>
            <span className="muted">{o.label}</span>
            <strong>{o.n ? `${o.avg_days.toFixed(1)} d` : "—"}</strong>
            <span className="faint">{o.n ? `median ${o.median_days.toFixed(0)} d · ${o.n} ${o.n === 1 ? "role" : "roles"}` : "no data yet"}</span>
          </div>
        ))}
        <Funnel f={rep.funnel} />
      </section>

      <StageChart stats={rep.stage_dwell} />

      <nav className={styles.tabs} aria-label="Filter applications">
        {(["open", "closed", "all"] as View[]).map((v) => (
          <Link key={v} href={href({ view: v })} className="chip" aria-current={view === v}>
            {v === "open" ? "Open" : v === "closed" ? "Closed" : "All"}{" "}
            <span className="faint">{v === "open" ? open.length : v === "closed" ? closed.length : rep.applications.length}</span>
          </Link>
        ))}
      </nav>

      <div className={styles.sortBar} role="toolbar" aria-label="Sort applications">
        <span className={styles.sortCol} />
        <span className={styles.sortCol}>{sortLink("company")}{sortLink("role")}</span>
        <span className={styles.sortCol}>{sortLink("stage")}{sortLink("days")}</span>
        <span className={styles.sortCol}>{sortLink("fit")}</span>
        <span className={styles.sortCol}>
          {sortLink("next")}{sortLink("applied")}
          {sortKey && <Link href={href({ sort: null })} scroll={false} className={styles.sortReset}>Reset</Link>}
        </span>
      </div>

      <ul className={styles.rows}>
        {sorted.map((a) => (
          <li key={a.id}>
            <Row a={a} href={href({ app: a.id })} selected={a.id === appId} />
          </li>
        ))}
        {sorted.length === 0 && <li className="muted">Nothing here.</li>}
      </ul>

      {selected && (
        <AnalysisDrawer a={selected} closeHref={href({})}
          siblings={rep.applications.filter((x) => companyKey(x.company) === companyKey(selected.company) && x.id !== selected.id)}
          siblingHref={(id) => href({ view: "all", app: id })} />
      )}
    </>
  );
}

function Row({ a, href, selected }: { a: AppAnalysis; href: string; selected: boolean }) {
  return (
    <Link href={href} scroll={false} className={styles.row} aria-current={selected}>
      <CompanyLogo company={a.company} stage={a.stage} />
      <span className={styles.who}>
        <strong>
          {a.company} <MailboxIcons accounts={a.accounts} />
          <DraftIcon state={a.draft_state} />
        </strong>
        <span className="muted">{a.role || "Role not detected"}</span>
      </span>
      <span className={styles.status}>
        <StageBadge stage={a.stage} />
        <span className="faint">{a.days_in_stage} d in stage</span>
      </span>
      <span className={styles.fit}>
        {a.fit ? <FitPill total={a.fit.total} band={a.fit.band} /> : <span className="faint">No fit score</span>}
      </span>
      <span className={styles.move} data-urgent={a.next_move.urgent}>
        {a.open ? a.next_move.title : a.days_to_outcome !== null ? `${STAGE_META[a.stage].label} after ${a.days_to_outcome} d` : "Closed"}
      </span>
    </Link>
  );
}

/** The cold email's state on the tile: a clock while Claude has it queued, an envelope when it's ready to read. */
function DraftIcon({ state }: { state: AppAnalysis["draft_state"] }) {
  if (state !== "queued" && state !== "ready") return null;
  const label = state === "queued" ? "Cold email queued for Claude" : "Cold email draft ready";
  return (
    <span className={styles.draftIcon} data-state={state} title={label}>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d={state === "queued" ? "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 6v6l4 2" : "M4 4h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zM22 6l-10 7L2 6"} />
      </svg>
      <span className="visually-hidden">{label}</span>
    </span>
  );
}

function Funnel({ f }: { f: AnalysisReport["funnel"] }) {
  const steps = [
    { label: "Applied", n: f.applied },
    { label: "Replied", n: f.responded },
    { label: "Shortlisted", n: f.shortlisted },
    { label: "Interviewed", n: f.interviewed },
    { label: "Offers", n: f.offers },
  ];
  return (
    <div className={`card ${styles.tile} ${styles.funnel}`}>
      <span className="muted">Funnel</span>
      <ol>
        {steps.map((s) => (
          <li key={s.label}>
            <span className={styles.funnelBar} style={{ width: `${f.applied ? Math.max(4, (s.n / f.applied) * 100) : 0}%` }} />
            <span>{s.label}</span>
            <strong>{s.n}</strong>
          </li>
        ))}
      </ol>
    </div>
  );
}

/** Average days spent in each stage before the next step, with the median as a tick. */
function StageChart({ stats }: { stats: DurationStat[] }) {
  if (stats.length === 0) {
    return (
      <section className={`card ${styles.chartCard}`}>
        <h2>Time spent in each stage</h2>
        <p className="muted">Appears once applications move past their first stage.</p>
      </section>
    );
  }
  const peak = Math.max(...stats.map((s) => Math.max(s.avg_days, s.median_days)), 1);
  const rowH = 34, left = 130, width = 560, top = 10;
  const x = (d: number) => left + (d / peak) * (width - left - 70);
  const longest = stats.reduce((m, s) => (s.avg_days > m.avg_days ? s : m), stats[0]);
  return (
    <section className={`card ${styles.chartCard}`} aria-labelledby="dwell-h">
      <h2 id="dwell-h">
        Longest wait: {longest.label.toLowerCase()} ({longest.avg_days.toFixed(1)} days on average)
      </h2>
      <svg viewBox={`0 0 ${width} ${top + stats.length * rowH + 8}`} role="img" aria-label="Average days in each stage" className={styles.chart}>
        {stats.map((s, i) => {
          const y = top + i * rowH;
          return (
            <g key={s.key}>
              <text x={left - 10} y={y + 17} textAnchor="end" className={styles.axisLabel}>{s.label}</text>
              <rect x={left} y={y + 6} width={Math.max(2, x(s.avg_days) - left)} height={16} rx={4}
                className={s.key === longest.key ? styles.barAccent : styles.bar}>
                <title>{`${s.label}: ${s.avg_days} days average, ${s.median_days} median, ${s.n} roles`}</title>
              </rect>
              <line x1={x(s.median_days)} x2={x(s.median_days)} y1={y + 3} y2={y + 25} className={styles.median} />
              <text x={x(s.avg_days) + 8} y={y + 18} className={styles.value}>
                {s.avg_days.toFixed(1)} d <tspan className={styles.n}>· n={s.n}</tspan>
              </text>
            </g>
          );
        })}
      </svg>
      <p className="faint">Bars show the average; the thin line is the median. Interview rounds are estimated from invitation emails.</p>
    </section>
  );
}
