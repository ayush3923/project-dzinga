import Link from "next/link";
import { Suspense } from "react";
import { api, type AnalysisReport, type AppAnalysis, type DurationStat } from "@/lib/api";
import { STAGE_META } from "@/lib/format";
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
  const sp = await searchParams;
  const v = one(sp.view);
  const view: View = v === "closed" || v === "all" ? v : "open";
  const appId = /^\d+$/.test(one(sp.app) ?? "") ? Number(one(sp.app)) : undefined;

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
  const sorted = [...list].sort((a, b) =>
    view === "open"
      ? Number(b.next_move.urgent) - Number(a.next_move.urgent) || b.days_in_stage - a.days_in_stage
      : Date.parse(b.stage_since) - Date.parse(a.stage_since),
  );
  const href = (patch: { view?: View; app?: number }) => {
    const q = new URLSearchParams({ view: patch.view ?? view });
    if (patch.app) q.set("app", String(patch.app));
    return `/analysis?${q}`;
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
          siblings={rep.applications.filter((x) => x.organization_id === selected.organization_id && x.id !== selected.id)}
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
        </strong>
        <span className="muted">{a.role || "Role not detected"}</span>
      </span>
      <span className={styles.status}>
        <StageBadge stage={a.stage} />
        <span className="faint">{a.days_in_stage} d in stage</span>
      </span>
      <span className={styles.fit}>
        {a.fit ? <FitPill total={a.fit.total} /> : <span className="faint">No fit score</span>}
      </span>
      <span className={styles.move} data-urgent={a.next_move.urgent}>
        {a.open ? a.next_move.title : a.days_to_outcome !== null ? `${STAGE_META[a.stage].label} after ${a.days_to_outcome} d` : "Closed"}
      </span>
    </Link>
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
