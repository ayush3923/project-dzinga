import Link from "next/link";
import { connection } from "next/server";
import { api, type DailyRun, type Profile } from "@/lib/api";
import { readiness, FULL_INTERVIEWS, WEEKLY_APPLY_TARGET, type Readiness } from "@/lib/readiness";
import styles from "./TopStatus.module.css";

const STEP: Record<string, string> = { refresh: "Mail refresh", quiz: "Daily quiz", diagnoses: "Diagnoses", interrupted: "Interrupted" };
const TRIGGER = { timer: "the 07:00 run", manual: "your refresh", claude: "Claude" } as const;
const STATE = { done: "success", running: "pending", partial: "failed" } as const;

const time = (iso: string) =>
  new Date(iso).toLocaleTimeString("en-IN", { hour: "2-digit", minute: "2-digit", timeZone: "Asia/Kolkata" });

/**
 * Top right of every page: today's daily run (an icon; details on hover), the
 * readiness index and the user's photo, linking to Profile. Each part hides
 * itself when its service isn't reachable.
 */
export async function TopStatus() {
  await connection(); // today's date and the latest run: render per request
  const [profile, run, ready] = await Promise.all([
    api.profile().catch(() => null),
    api.runs(1).then((r) => r[0] ?? null).catch(() => null),
    loadReadiness().catch(() => null),
  ]);
  return (
    <div className={styles.bar}>
      {run && <RunStatus run={run} />}
      <ProfileChip profile={profile} ready={ready} />
    </div>
  );
}

async function loadReadiness(): Promise<Readiness> {
  const [stats, digest, sum] = await Promise.all([api.quizStats(), api.digest(), api.summary()]);
  const today = new Date().toLocaleDateString("en-CA", { timeZone: "Asia/Kolkata" });
  return readiness({
    accuracy7d: stats.accuracy_7d,
    followThrough7d: stats.follow_through_7d,
    quizDays: stats.days,
    applied7d: digest.series.slice(-7).reduce((n, d) => n + d.applications, 0),
    activeInterviews: sum.by_stage.interview + sum.by_stage.assessment,
    today,
  });
}

function RunStatus({ run }: { run: DailyRun }) {
  const state = STATE[run.status];
  const label = `Daily run ${state === "success" ? "succeeded" : state === "pending" ? "in progress" : "had a problem"}`;
  return (
    <div className={styles.pop}>
      <button type="button" className={styles.iconBtn} data-state={state} aria-label={label} aria-describedby="run-pop">
        {state === "success" ? (
          <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
        ) : state === "pending" ? (
          <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" className={styles.spin}><path d="M8 2.5a5.5 5.5 0 1 1-5.5 5.5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
        ) : (
          <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="M8 3.5v5.5M8 12v.5" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" /></svg>
        )}
      </button>
      <div id="run-pop" role="tooltip" className={styles.panel}>
        <div className={styles.panelHead}>
          <span className={styles.title}>Daily run</span>
          <span className={styles.pill} data-state={state}>{state}</span>
        </div>
        <p className={styles.sub}>{run.day} · {time(run.started_at)} · by {TRIGGER[run.trigger]}</p>
        <ul className={styles.steps}>
          {run.steps.map((s) => (
            <li key={s.name} data-status={s.status}>
              <span className={styles.stepName}>{STEP[s.name] ?? s.name}</span>
              <span className={styles.stepDetail}>{s.detail || s.status}</span>
            </li>
          ))}
          {run.steps.length === 0 && <li className={styles.sub}>Starting…</li>}
        </ul>
      </div>
    </div>
  );
}

/** A ring that fills with the readiness index, the number inside. */
function Ring({ value }: { value: number }) {
  const r = 13;
  const c = 2 * Math.PI * r;
  return (
    <span className={styles.ring} aria-hidden="true">
      <svg viewBox="0 0 32 32" width="32" height="32">
        <circle cx="16" cy="16" r={r} className={styles.ringTrack} />
        <circle cx="16" cy="16" r={r} className={styles.ringValue} strokeDasharray={`${(value / 100) * c} ${c}`} transform="rotate(-90 16 16)" />
      </svg>
      <span>{value}</span>
    </span>
  );
}

function ProfileChip({ profile, ready }: { profile: Profile | null; ready: Readiness | null }) {
  const initials = (profile?.full_name ?? "").split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]).join("").toUpperCase();
  return (
    <div className={styles.pop}>
      <Link href="/profile" className={styles.chip}
        aria-label={ready ? `Readiness ${ready.index} of 100. Open your profile` : "Open your profile"}
        aria-describedby={ready ? "ready-pop" : undefined}>
        {ready && <Ring value={ready.index} />}
        {profile?.photo_updated_at ? (
          // eslint-disable-next-line @next/next/no-img-element -- a small local image from core-api
          <img src={`/profile/photo?v=${encodeURIComponent(profile.photo_updated_at)}`} alt="" className={styles.avatar} width={32} height={32} />
        ) : (
          <span className={styles.avatar} aria-hidden="true">{initials || "?"}</span>
        )}
      </Link>
      {ready && (
        <div id="ready-pop" role="tooltip" className={styles.panel}>
          <div className={styles.panelHead}>
            <span className={styles.title}>Readiness</span>
            <span className={styles.score}>{ready.index}<small>/100</small></span>
          </div>
          <p className={styles.sub}>Three equal parts, from the last 7 days.</p>
          <dl className={styles.parts}>
            <Part name="Quiz" value={ready.quiz}
              detail={`${ready.detail.accuracy7d ?? 0}% right · ${ready.detail.quizDays7d} of 7 days${ready.detail.followThrough7d !== null ? ` · drills ${ready.detail.followThrough7d}% followed` : ""}`} />
            <Part name="Apply momentum" value={ready.momentum} detail={`${ready.detail.applied7d} applied · target ${WEEKLY_APPLY_TARGET} a week`} />
            <Part name="Interviews" value={ready.interviews} detail={`${ready.detail.activeInterviews} live · full at ${FULL_INTERVIEWS}`} />
          </dl>
        </div>
      )}
    </div>
  );
}

function Part({ name, value, detail }: { name: string; value: number; detail: string }) {
  return (
    <div>
      <dt>{name}</dt>
      <dd>{value}</dd>
      <span className={styles.meter} aria-hidden="true"><i style={{ width: `${value}%` }} /></span>
      <span className={styles.sub}>{detail}</span>
    </div>
  );
}
