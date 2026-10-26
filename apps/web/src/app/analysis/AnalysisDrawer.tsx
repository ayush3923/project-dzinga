import Link from "next/link";
import { api, type AppAnalysis, type AppDetail, type FactorKey } from "@/lib/api";
import { parsePipeline, shortDate } from "@/lib/format";
import { CompanyLogo, MailboxIcons, StageBadge } from "@/components/ui";
import { EditDetails } from "@/components/EditDetails";
import { FitPill } from "./FitPill";
import { DraftPanel, FitButton } from "./AgentActions";
import { ResearchButton } from "@/components/ResearchButton";
import styles from "./analysis.module.css";

const STAGE_LABEL: Record<string, string> = {
  applied: "applied", assessment: "assessment", interview: "interviewing", offer: "offer", rejected: "closed",
};

const FACTOR: Record<FactorKey, string> = {
  experience: "Experience",
  location: "Location",
  seniority: "Seniority",
  role_alignment: "Alignment with the role",
  interest_intent: "Interest and intent",
};

export async function AnalysisDrawer({ a, closeHref, siblings, siblingHref }: {
  a: AppAnalysis;
  closeHref: string;
  siblings: AppAnalysis[];
  siblingHref: (id: number) => string;
}) {
  let d: AppDetail;
  let profileReady: boolean;
  try {
    const [detail, profile] = await Promise.all([api.detail(a.id), api.profile()]);
    d = detail;
    profileReady = profile.updated_at !== null;
  } catch {
    return null;
  }

  return (
    <>
      <Link href={closeHref} scroll={false} className={styles.scrim} aria-label="Close details" />
      <aside className={styles.drawer} aria-labelledby="an-title">
        <div className={styles.drawerHead}>
          <CompanyLogo company={a.company} stage={a.stage} />
          <div>
            <h2 id="an-title" className="display">{a.company}</h2>
            <p className="muted">
              {a.role || "Role not detected"} <MailboxIcons accounts={a.accounts} />
            </p>
          </div>
          <Link href={closeHref} scroll={false} className={styles.close} aria-label="Close">✕</Link>
        </div>

        <EditDetails id={a.id} company={a.company} role={a.role} jobUrl={a.job_url} basePath="/analysis?view=all&app=" />

        {/* 3. Current status and next move */}
        <section className={styles.section}>
          <div className={styles.statusLine}>
            <StageBadge stage={a.stage} />
            <span>
              for <strong>{a.days_in_stage} days</strong> (since {shortDate(a.stage_since)})
            </span>
          </div>
          {a.open && (
            <div className={styles.nextMove} data-urgent={a.next_move.urgent}>
              <strong>Next move: {a.next_move.title}</strong>
              <p>{a.next_move.detail}</p>
            </div>
          )}
        </section>

        {/* 4. Journey and time to decision */}
        <section className={styles.section}>
          <h3>
            Journey
            {a.days_to_outcome !== null && (
              <span className="faint"> · {a.outcome === "offer" ? "offer" : "decision"} after {a.days_to_outcome} days</span>
            )}
          </h3>
          <ol className={styles.journey}>
            {a.timeline.map((s, i) => (
              <li key={i} data-stage={s.stage === "shortlisted" ? "assessment" : s.stage}>
                {s.days_from_prev !== null && <span className={styles.gap}>+{s.days_from_prev} d</span>}
                <span className={styles.stepDot} />
                <span className={styles.stepLabel}>{s.label}</span>
                <span className="faint">{shortDate(s.at)}{s.source === "manual" ? " · set by you" : ""}</span>
              </li>
            ))}
            {a.open && a.outcome !== "offer" && (
              <li className={styles.pending}>
                <span className={styles.gap}>+{a.days_in_stage} d so far</span>
                <span className={styles.stepDot} />
                <span className={styles.stepLabel}>Decision</span>
                <span className="faint">pending</span>
              </li>
            )}
          </ol>
        </section>

        {/* 2. Fit score */}
        <section className={styles.section}>
          <h3>Fit score</h3>
          {!profileReady ? (
            <p className="muted">
              Fit scores compare each posting with your profile. <Link href="/profile" className={styles.link}>Add your profile</Link> first.
            </p>
          ) : !d.fit ? (
            <>
              <p className="muted">
                {a.job_url || d.job_links.length
                  ? "Reads the posting from its link and scores it against your profile and intent on five factors, with rules. Claude Sonnet 5 writes a one-line verdict and tips (about ₹0.10)."
                  : "No posting link yet. Add one with Edit details, or queue Claude in the app to find it."}
              </p>
              <div style={{ display: "flex", gap: 12, alignItems: "center", flexWrap: "wrap" }}>
                {(a.job_url || d.job_links.length > 0 || d.posting?.status === "found") && <FitButton id={a.id} label="Score this role" />}
                <ResearchButton kind="applications" id={a.id} label="Find the posting" requestedAt={d.research_requested_at} quiet={Boolean(a.job_url || d.job_links.length)} />
              </div>
            </>
          ) : (
            <>
              <div className={styles.fitHead}>
                <FitPill total={d.fit.total} large />
                <p>{d.fit.verdict}</p>
              </div>
              <ul className={styles.factors}>
                {d.fit.factors.map((f) => (
                  <li key={f.factor}>
                    <details>
                      <summary>
                        <span>{FACTOR[f.factor]}</span>
                        <span className={styles.factorBar}><span style={{ width: `${(f.score / 20) * 100}%` }} /></span>
                        <strong>{f.score}<small>/20</small></strong>
                      </summary>
                      <p>{f.rationale}</p>
                    </details>
                  </li>
                ))}
              </ul>
              {d.fit.evidence.length > 0 && (
                <details className={styles.more}>
                  <summary>
                    Requirement match: {d.fit.evidence.filter((e) => e.strength === "strong").length} strong ·{" "}
                    {d.fit.evidence.filter((e) => e.strength === "partial").length} partial ·{" "}
                    {d.fit.evidence.filter((e) => e.strength === "missing").length} missing
                  </summary>
                  <ul className={styles.evidence}>
                    {d.fit.evidence.map((e, i) => (
                      <li key={i} data-strength={e.strength}>
                        <span className={styles.strength}>{e.strength}</span>
                        <span>
                          <strong>{e.requirement}</strong>
                          {e.evidence && <span className="muted"> · {e.evidence}</span>}
                        </span>
                      </li>
                    ))}
                  </ul>
                </details>
              )}
              {(d.fit.strengths.length > 0 || d.fit.gaps.length > 0) && (
                <div className={styles.sg}>
                  {d.fit.strengths.length > 0 && (
                    <div><p className={styles.listTitle}>Strengths</p><ul>{d.fit.strengths.map((t, i) => <li key={i}>{t}</li>)}</ul></div>
                  )}
                  {d.fit.gaps.length > 0 && (
                    <div><p className={styles.listTitle}>Gaps</p><ul>{d.fit.gaps.map((t, i) => <li key={i}>{t}</li>)}</ul></div>
                  )}
                </div>
              )}
              {d.fit.suggestions.length > 0 && (
                <details className={styles.more}>
                  <summary>How to raise your chances ({d.fit.suggestions.length})</summary>
                  <ul>{d.fit.suggestions.map((s, i) => <li key={i}>{s}</li>)}</ul>
                </details>
              )}
              {d.posting && (
                <details className={styles.more}>
                  <summary>
                    The posting{d.posting.status === "found" ? "" : " (not found; scored on what is known)"}
                  </summary>
                  {d.posting.url && (
                    <p>
                      <a href={d.posting.url} target="_blank" rel="noopener noreferrer" className={styles.link}>
                        {d.posting.title || "Open the posting"}
                      </a>{" "}
                      <span className="faint">
                        via {a.job_url && d.posting.url === a.job_url ? "your link" : d.posting.source === "email_link" ? "your email" : "web search"}
                      </span>
                    </p>
                  )}
                  <p className="faint">
                    {[d.posting.location, d.posting.work_mode, d.posting.seniority, d.posting.min_years !== null ? `${d.posting.min_years}+ years` : ""]
                      .filter(Boolean).join(" · ")}
                  </p>
                  {d.posting.product_context && <p>{d.posting.product_context}</p>}
                  <PostingList title="Responsibilities" items={d.posting.responsibilities} />
                  <PostingList title="Requirements" items={d.posting.requirements} />
                  <PostingList title="Preferred" items={d.posting.qualifications} />
                </details>
              )}
              <p className="faint">
                Scored {shortDate(d.fit.created_at)}
                {d.fit.pipeline
                  ? <> · {parsePipeline(d.fit.pipeline).map((s) => `${s.task}: ${s.model}`).join(" · ")}</>
                  : <> by {d.fit.model}</>}
                . <FitButton id={a.id} label="Re-score" quiet />
              </p>
            </>
          )}
        </section>

        {/* 1. Other applications to the same organization */}
        <section className={styles.section}>
          <h3>
            {siblings.length === 0 ? `Your only application to ${a.company}` : `Other applications to ${a.company} (${siblings.length})`}
          </h3>
          {siblings.length > 0 && (
            <ul className={styles.contacts}>
              {siblings.map((s) => (
                <li key={s.id}>
                  <Link href={siblingHref(s.id)} scroll={false} className={styles.link}>{s.role || "Role not detected"}</Link>{" "}
                  <span className="faint">· {STAGE_LABEL[s.stage]} · applied {shortDate(s.applied_at)}</span>
                </li>
              ))}
            </ul>
          )}
          <Link href={`/organizations?org=${a.organization_id}`} className={styles.link}>Open {a.company} in Organizations →</Link>
        </section>

        {/* People */}
        <section className={styles.section}>
          <h3>People</h3>
          {d.posting?.hiring_manager_name && (
            <p>
              <strong>{d.posting.hiring_manager_name}</strong>, {d.posting.hiring_manager_title}{" "}
              <span className="faint">· likely hiring manager, named in the posting (email unknown)</span>
            </p>
          )}
          {d.contacts.length === 0 && !d.posting?.hiring_manager_name ? (
            <p className="muted">Only automated senders so far. Nobody at {a.company} has emailed you personally.</p>
          ) : (
            <ul className={styles.contacts}>
              {d.contacts.map((c) => (
                <li key={c.id}>
                  <strong>{c.name || c.email}</strong>
                  {c.name && <span className="muted"> {c.email}</span>}
                  <span className="faint"> · {c.role_hint || "contact"}, emailed you {shortDate(c.last_seen_at)}</span>
                </li>
              ))}
            </ul>
          )}
          <p className="faint">Dzinga only shows people who emailed you or who are named in the posting. It never guesses email addresses.</p>
        </section>

        {/* Cold email: on request only */}
        {a.open && (
          <section className={styles.section}>
            <h3>Cold email</h3>
            <DraftPanel
              id={a.id}
              company={a.company}
              disabled={!profileReady}
              contacts={d.contacts.map((c) => ({ id: c.id, label: c.name ? `${c.name} (${c.email})` : c.email }))}
              draft={d.drafts[0] ?? null}
            />
          </section>
        )}
      </aside>
    </>
  );
}

function PostingList({ title, items }: { title: string; items: string[] }) {
  if (items.length === 0) return null;
  return (
    <>
      <p className={styles.listTitle}>{title}</p>
      <ul>{items.map((t, i) => <li key={i}>{t}</li>)}</ul>
    </>
  );
}
