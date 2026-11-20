import Link from "next/link";
import { connection } from "next/server";
import { Suspense } from "react";
import { api, type Drill, type PrepQuestion, type QuizPage, type QuizQuestion, type QuizStats, type QuizTopic } from "@/lib/api";
import { Offline } from "@/components/ui";
import { McqQuestion, VoiceQuestion, WrittenQuestion } from "./QuizControls";
import styles from "./quiz.module.css";

const TOPIC: Record<QuizTopic, string> = { ai_news: "AI news", pm_concept: "Product concept", org_news: "Company news" };
const KIND = { mcq: "Multiple choice", subjective: "Written answer", voice: "Spoken answer" } as const;
const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);
const shortDay = (day: string) =>
  new Date(`${day}T00:00:00+05:30`).toLocaleDateString("en-IN", { weekday: "short", day: "numeric", month: "short", timeZone: "Asia/Kolkata" });
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** A score out of 10, as a tone: good (8+), fair (5–7) or low. */
const tone = (score: number) => (score >= 8 ? "good" : score >= 5 ? "fair" : "low");

/**
 * Preparation: coaching from your feedback first, then whatever needs doing (the
 * quiz while it's open, interview rehearsal once it's done), with finished work
 * folded away.
 */
export default function PreparationRoute({ searchParams }: PageProps<"/quiz">) {
  return (
    <Suspense fallback={<div className={styles.page}><div className="skeleton" style={{ height: 64 }} /><div className="skeleton" style={{ height: 420 }} /></div>}>
      <Preparation searchParams={searchParams} />
    </Suspense>
  );
}

async function Preparation({ searchParams }: { searchParams: PageProps<"/quiz">["searchParams"] }) {
  await connection(); // "today" depends on the date: render per request, before any fetch
  const d = one((await searchParams).day);
  let data: QuizPage;
  try {
    data = await api.quiz(d && /^\d{4}-\d{2}-\d{2}$/.test(d) ? d : undefined);
  } catch {
    return <Offline />;
  }
  const [stats, drills, prep] = await Promise.all([
    api.quizStats().catch(() => null),
    api.drills().catch(() => null),
    api.prep().catch(() => null),
  ]);
  const today = new Date().toLocaleDateString("en-CA", { timeZone: "Asia/Kolkata" });
  const qs = data.quiz?.questions ?? [];
  const left = qs.filter((x) => !x.response).length;
  const quizOpen = qs.length > 0 && left > 0;
  const isToday = data.day === today;
  const questions = prep?.items ?? [];

  // One line on where things stand, in plain words.
  const status = [
    !data.quiz ? "today's quiz is on its way" : quizOpen ? `${plural(left, "question", "questions")} left in today's quiz` : "today's quiz is done",
    questions.length > 0 && `${plural(questions.length, "interview question", "interview questions")} to rehearse`,
    drills && drills.open.length > 0 && `${plural(drills.open.length, "coaching point", "coaching points")} to apply`,
  ].filter(Boolean).join(" · ");

  const quiz = <QuizSection data={data} isToday={isToday} />;
  const rehearse = questions.length > 0 && <Rehearse items={questions} day={prep!.day} today={today} />;

  return (
    <div className={styles.page}>
      <header className={styles.hero}>
        <div>
          <h1 className="display">Preparation</h1>
          <p className={styles.status}>{status.charAt(0).toUpperCase() + status.slice(1)}.</p>
        </div>
        {stats && <Stats s={stats} />}
      </header>

      {drills && (drills.open.length > 0 || drills.recent.length > 0) && <Coaching open={drills.open} recent={drills.recent} />}
      {quizOpen || !isToday ? <>{quiz}{rehearse}</> : <>{rehearse}{quiz}</>}
    </div>
  );
}

/** Three small numbers beside the title: accuracy, coaching followed, streak. */
function Stats({ s }: { s: QuizStats }) {
  return (
    <dl className={styles.stats}>
      <div>
        <dt>Accuracy</dt>
        <dd>{s.accuracy_7d !== null ? `${s.accuracy_7d}%` : "–"}</dd>
        <span>last 7 days</span>
      </div>
      <div>
        <dt>Coaching followed</dt>
        <dd>{s.follow_through_7d !== null ? `${s.follow_through_7d}%` : "–"}</dd>
        <span>{s.follow_through_7d !== null ? `${plural(s.drills_checked_7d, "point", "points")} checked` : "not checked yet"}</span>
      </div>
      <div>
        <dt>Streak</dt>
        <dd>{s.streak}</dd>
        <span>{s.streak === 1 ? "day" : "days"}</span>
      </div>
    </dl>
  );
}

// ---------- Section header ----------

const ICON = {
  coach: "M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z",
  rehearse: "M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3zM19 10v2a7 7 0 0 1-14 0v-2M12 19v3",
  quiz: "M9 11l3 3L22 4M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11",
} as const;

/** Each section's header: its icon in the section colour, the title and one line of context. */
function SectionHead({ id, icon, title, children }: { id: string; icon: keyof typeof ICON; title: string; children: React.ReactNode }) {
  return (
    <div className={styles.sectionHead}>
      <span className={styles.secIcon} aria-hidden="true">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          <path d={ICON[icon]} />
        </svg>
      </span>
      <div>
        <h2 id={id}>{title}</h2>
        <p>{children}</p>
      </div>
    </div>
  );
}

// ---------- Coaching ----------

const STATUS_LABEL = { met: "Done", partly: "Partly", missed: "Missed", open: "Open" } as const;

/** Coaching points from feedback, split by the kind of answer they apply to. */
function Coaching({ open, recent }: { open: Drill[]; recent: Drill[] }) {
  const set = open.reduce((min, x) => (x.day < min ? x.day : min), open[0]?.day ?? "");
  const written = open.filter((x) => x.kind === "subjective");
  const spoken = open.filter((x) => x.kind === "voice");
  const both = open.filter((x) => x.kind === "any");
  const done = recent.filter((x) => x.status === "met").length;
  return (
    <section className={`card ${styles.section}`} data-section="coach" aria-labelledby="coach-h">
      <SectionHead id="coach-h" icon="coach" title="Coaching points">
        {open.length > 0 ? <>From your feedback on {shortDay(set)}. Your next answers are checked against them.</> : "All caught up."}
        {recent.length > 0 && <> {done} of {recent.length} applied in the last two weeks.</>}
      </SectionHead>
      {open.length > 0 && (
        <div className={styles.coachCols}>
          <CoachList title="Written answers" items={written} />
          <CoachList title="Spoken answers" items={spoken} />
        </div>
      )}
      {both.length > 0 && (
        <p className={styles.coachBoth}><span className={styles.tag}>Both</span> {both.map((x) => x.text).join(" · ")}</p>
      )}
      {recent.length > 0 && (
        <details className={styles.more}>
          <summary>Recently checked ({recent.length})</summary>
          <ul className={styles.checked}>
            {recent.map((x) => (
              <li key={x.id}>
                <span className={styles.pill} data-tone={x.status === "met" ? "good" : x.status === "partly" ? "fair" : "low"}>{STATUS_LABEL[x.status]}</span>
                <span>{x.text}{x.check_note && <span className="faint"> — {x.check_note}</span>}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}

function CoachList({ title, items }: { title: string; items: Drill[] }) {
  if (items.length === 0) return null;
  return (
    <div>
      <h3 className={styles.label}>{title}</h3>
      <ol className={styles.coach}>
        {items.map((x) => <li key={x.id}>{x.text}</li>)}
      </ol>
    </div>
  );
}

// ---------- Interview rehearsal ----------

function Rehearse({ items, day, today }: { items: PrepQuestion[]; day: string; today: string }) {
  return (
    <section className={styles.block} data-section="rehearse" aria-labelledby="rehearse-h">
      <SectionHead id="rehearse-h" icon="rehearse" title="Rehearse for your interviews">
        Questions likely in your live processes{day !== today && `, from ${shortDay(day)}`}. Answer out loud first, then open the approach.
      </SectionHead>
      <ol className={styles.rehearse}>
        {items.map((q, i) => (
          <li key={i} className="card">
            {q.company && (
              <p className={styles.for}>
                <strong>{q.company}</strong>{q.role && <span> · {q.role}</span>}
              </p>
            )}
            <p className={styles.question}>{q.question}</p>
            <p className={styles.why}><span>Why it may come up:</span> {q.why}</p>
            <details className={styles.more}>
              <summary>How to approach it</summary>
              <p className={styles.approach}>{q.approach}</p>
            </details>
          </li>
        ))}
      </ol>
    </section>
  );
}

// ---------- Daily quiz ----------

function QuizSection({ data, isToday }: { data: QuizPage; isToday: boolean }) {
  const q = data.quiz;
  const qs = q?.questions ?? [];
  const mcqs = qs.filter((x) => x.kind === "mcq");
  const right = mcqs.filter((x) => x.response?.correct).length;
  const answeredMcq = mcqs.filter((x) => x.response).length;
  const done = qs.length > 0 && qs.every((x) => x.response);
  const scored = qs.filter((x) => x.kind !== "mcq" && x.response?.score !== null && x.response?.score !== undefined);

  return (
    <section className={`card ${styles.section}`} data-section="quiz" aria-labelledby="quiz-h">
      <SectionHead id="quiz-h" icon="quiz" title={isToday ? "Today's quiz" : `Quiz for ${shortDay(data.day)}`}>
        {!q ? (data.queued_at ? "Claude is building it from today's news and your profile." : "No quiz for this day.")
          : done ? "Done." : `${qs.filter((x) => x.response).length} of ${qs.length} answered.`}
        {answeredMcq > 0 && <> {right} of {answeredMcq} multiple choice right.</>}
      </SectionHead>

      {q && done && (
        <>
          <ul className={styles.results} aria-label="Results">
            {qs.map((x, i) => <ResultChip key={x.id} x={x} n={i + 1} />)}
          </ul>
          {scored.length > 0 && (
            <p className={styles.note}>
              {scored.map((x) => `${x.kind === "voice" ? "Spoken" : "Written"} answer ${x.response!.score}/10`).join(" · ")}. Open them to read the feedback.
            </p>
          )}
          <details className={styles.more}>
            <summary>Review answers and feedback</summary>
            <ol className={styles.questions}>{qs.map((x, i) => <Question key={x.id} x={x} n={i + 1} />)}</ol>
          </details>
        </>
      )}
      {q && !done && <ol className={styles.questions}>{qs.map((x, i) => <Question key={x.id} x={x} n={i + 1} />)}</ol>}

      {data.history.length > 1 && (
        <nav className={styles.history} aria-label="Earlier quizzes">
          <span className="faint">Earlier</span>
          {data.history.map((h) => (
            <Link key={h.day} href={h.day === data.day ? "/quiz" : `/quiz?day=${h.day}`} aria-current={h.day === data.day ? "page" : undefined}>
              {shortDay(h.day)} <span>{h.correct}/5</span>
            </Link>
          ))}
        </nav>
      )}
    </section>
  );
}

/** One result in the done-quiz summary: right or wrong for multiple choice, the score for written and spoken. */
function ResultChip({ x, n }: { x: QuizQuestion; n: number }) {
  const r = x.response;
  const hasScore = r?.score !== null && r?.score !== undefined;
  const label = x.kind === "mcq" ? (r?.correct ? "Right" : "Wrong") : hasScore ? `${r!.score}/10` : "Awaiting feedback";
  const t = x.kind === "mcq" ? (r?.correct ? "good" : "low") : hasScore ? tone(r!.score!) : "neutral";
  return (
    <li className={styles.result} data-tone={t} title={x.prompt}>
      <span className={styles.resultN}>{n}</span>
      <span className={styles.resultBody}>
        <span className={styles.resultTopic}>{TOPIC[x.topic]}</span>
        <strong>{label}</strong>
      </span>
    </li>
  );
}

function Question({ x, n }: { x: QuizQuestion; n: number }) {
  return (
    <li className={styles.q}>
      <div className={styles.qHead}>
        <span className={styles.qN}>{n}</span>
        <span className="faint">{TOPIC[x.topic]} · {KIND[x.kind]}</span>
      </div>
      <p className={styles.prompt}>{x.prompt}</p>
      {x.source_url && (
        <a href={x.source_url} target="_blank" rel="noopener noreferrer" className={styles.source}>
          {x.source_title || new URL(x.source_url).hostname} ↗
        </a>
      )}
      {x.kind === "mcq" && <McqQuestion q={x} />}
      {x.kind === "subjective" && <WrittenQuestion q={x} />}
      {x.kind === "voice" && <VoiceQuestion q={x} />}
    </li>
  );
}
