import Link from "next/link";
import { Suspense } from "react";
import { api, type OrgsReport, type OrgSummary } from "@/lib/api";
import { FORTUNE_500_PARTIAL } from "@/lib/fortune500";
import { shortDate } from "@/lib/format";
import { CompanyLogo, EmptyInbox, Offline } from "@/components/ui";
import { PreferredEditor } from "./PreferredEditor";
import { OrgDrawer } from "./OrgDrawer";
import styles from "./organizations.module.css";

const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default function OrganizationsPage({ searchParams }: PageProps<"/organizations">) {
  return (
    <div className={styles.page}>
      <header className={styles.head}>
        <p className="eyebrow">Organizations</p>
        <h1 className="display">Every company, every application</h1>
      </header>
      <Suspense fallback={<div className="skeleton" style={{ height: 480 }} />}>
        <Report searchParams={searchParams} />
      </Suspense>
    </div>
  );
}

async function Report({ searchParams }: { searchParams: PageProps<"/organizations">["searchParams"] }) {
  const sp = await searchParams;
  const orgId = /^\d+$/.test(one(sp.org) ?? "") ? Number(one(sp.org)) : undefined;
  let rep: OrgsReport;
  try {
    rep = await api.organizations();
  } catch {
    return <Offline />;
  }
  if (rep.organizations.length === 0) return <EmptyInbox />;
  const multi = rep.organizations.filter((o) => o.applications > 1).length;

  return (
    <>
      <p className={styles.lead}>
        {rep.organizations.filter((o) => o.applications > 0).length} organizations · {multi} with more than one application
      </p>

      <section aria-labelledby="pref-h" className={styles.preferred}>
        <div className={styles.sectionHead}>
          <h2 id="pref-h">Preferred</h2>
          <span className="faint">{rep.preferred_mode === "auto" ? "your top 3 by applications" : "chosen by you"}</span>
        </div>
        <div className={styles.prefGrid}>
          {rep.preferred.map((o) => <PreferredCard key={o.id} o={o} />)}
        </div>
        <PreferredEditor
          mode={rep.preferred_mode}
          current={rep.preferred.map((o) => o.name)}
          suggestions={[...new Set([...rep.organizations.map((o) => o.name), ...FORTUNE_500_PARTIAL])]}
        />
      </section>

      <section aria-labelledby="all-h">
        <div className={styles.sectionHead}>
          <h2 id="all-h">All organizations</h2>
          <span className="faint">most recent activity first</span>
        </div>
        <div className={styles.table} role="table">
          <div className={styles.thead} role="row">
            <span role="columnheader">Organization</span>
            <span role="columnheader">Applications</span>
            <span role="columnheader">Outcomes</span>
            <span role="columnheader">First applied</span>
            <span role="columnheader">Avg. response</span>
          </div>
          {rep.organizations.map((o) => (
            <Link key={o.id} href={`/organizations?org=${o.id}`} scroll={false} className={styles.tr} role="row" aria-current={o.id === orgId}>
              <span role="cell" className={styles.name}>
                <CompanyLogo company={o.name} />
                <strong>{o.name}</strong>
                {o.preferred && <span className={styles.star} title="Preferred">★</span>}
              </span>
              <span role="cell">{o.applications} <span className="faint">({o.open} open)</span></span>
              <span role="cell"><Outcomes o={o} /></span>
              <span role="cell" className="muted">{o.first_applied_at ? shortDate(o.first_applied_at) : "—"}</span>
              <span role="cell">
                {o.avg_response_days !== null ? `${o.avg_response_days} d` : <span className="faint">no reply</span>}
                {o.diagnosis_suggested && <span className={styles.flag} title={o.diagnosis_reason}>needs a look</span>}
              </span>
            </Link>
          ))}
        </div>
      </section>

      {orgId && <OrgDrawer id={orgId} />}
    </>
  );
}

function PreferredCard({ o }: { o: OrgSummary }) {
  return (
    <Link href={`/organizations?org=${o.id}`} scroll={false} className={`card ${styles.prefCard}`}>
      <div className={styles.prefTop}>
        <CompanyLogo company={o.name} />
        <strong>{o.name}</strong>
      </div>
      {o.applications === 0 ? (
        <p className="faint">No applications yet</p>
      ) : (
        <>
          <p className={styles.prefStat}>
            <strong>{o.applications}</strong> {o.applications === 1 ? "application" : "applications"} · {o.open} open
          </p>
          <Outcomes o={o} />
          <p className="faint">
            Since {o.first_applied_at ? shortDate(o.first_applied_at) : "—"}
            {o.avg_response_days !== null && <> · replies in {o.avg_response_days} d</>}
          </p>
        </>
      )}
      {o.diagnosis_suggested && <p className={styles.flagLine}>{o.diagnosis_reason}</p>}
    </Link>
  );
}

/** A small stacked bar: open, in process, offers, rejected. */
function Outcomes({ o }: { o: OrgSummary }) {
  if (o.applications === 0) return <span className="faint">—</span>;
  const inProcess = Math.max(0, o.interviewed - o.offers);
  const waiting = Math.max(0, o.open - inProcess - o.offers);
  const parts = [
    { key: "applied", n: waiting, label: "waiting" },
    { key: "interview", n: inProcess, label: "interviewing" },
    { key: "offer", n: o.offers, label: "offers" },
    { key: "rejected", n: o.rejected, label: "rejected" },
  ].filter((p) => p.n > 0);
  return (
    <span className={styles.outcomes} title={parts.map((p) => `${p.n} ${p.label}`).join(", ")}>
      <span className={styles.bar}>
        {parts.map((p) => <span key={p.key} data-stage={p.key} style={{ flexGrow: p.n }} />)}
      </span>
      <span className="faint">{parts.map((p) => `${p.n} ${p.label}`).join(" · ")}</span>
    </span>
  );
}
