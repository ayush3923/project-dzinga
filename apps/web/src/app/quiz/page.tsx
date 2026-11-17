import Link from "next/link";
import { Suspense } from "react";
import { api, type QuizPage, type QuizQuestion, type QuizTopic } from "@/lib/api";
import { Offline } from "@/components/ui";
import { McqQuestion, VoiceQuestion, WrittenQuestion } from "./QuizControls";
import styles from "./quiz.module.css";

const TOPIC: Record<QuizTopic, string> = { ai_news: "AI news", pm_concept: "Product concept", org_news: "Company news" };
const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);

export default function QuizRoute({ searchParams }: PageProps<"/quiz">) {
  return (
    <div className={styles.page}>
      <header>
        <p className="eyebrow">Daily quiz</p>
        <h1 className="display">Five quick ones, one to write, one to say</h1>
      </header>
      <Suspense fallback={<div className="skeleton" style={{ height: 480 }} />}>
        <Quiz searchParams={searchParams} />
      </Suspense>
    </div>
  );
}

function dayLabel(day: string): string {
  return new Date(`${day}T00:00:00+05:30`).toLocaleDateString("en-IN", { weekday: "long", day: "numeric", month: "long", timeZone: "Asia/Kolkata" });
}

async function Quiz({ searchParams }: { searchParams: PageProps<"/quiz">["searchParams"] }) {
  const d = one((await searchParams).day);
  let data: QuizPage;
  try {
    data = await api.quiz(d && /^\d{4}-\d{2}-\d{2}$/.test(d) ? d : undefined);
  } catch {
    return <Offline />;
  }
  const q = data.quiz;
  const mcqs = q?.questions.filter((x) => x.kind === "mcq") ?? [];
  const correct = mcqs.filter((x) => x.response?.correct).length;
  const answered = mcqs.filter((x) => x.response).length;

  return (
    <>
      <p className={styles.lead}>
        {dayLabel(data.day)}
        {q && answered > 0 && <> · {correct} of {answered} right so far</>}
      </p>

      {!q ? (
        <section className={`card ${styles.empty}`}>
          {data.queued_at ? (
            <>
              <h2>Being prepared</h2>
              <p className="muted">
                This quiz is queued for Claude. It&rsquo;s built from today&rsquo;s AI news, product concepts from your
                profile, and news about companies you applied to, on the next scheduled run.
              </p>
            </>
          ) : (
            <>
              <h2>No quiz for this day</h2>
              <p className="muted">A quiz is queued with each day&rsquo;s first refresh.</p>
            </>
          )}
        </section>
      ) : (
        <ol className={styles.questions}>
          {q.questions.map((x, i) => (
            <li key={x.id} className={`card ${styles.question}`}>
              <QuestionHead x={x} n={i + 1} />
              {x.kind === "mcq" && <McqQuestion q={x} />}
              {x.kind === "subjective" && <WrittenQuestion q={x} />}
              {x.kind === "voice" && <VoiceQuestion q={x} />}
            </li>
          ))}
        </ol>
      )}

      {data.history.length > 0 && (
        <nav className={styles.history} aria-label="Earlier quizzes">
          <h2>Earlier quizzes</h2>
          <ul>
            {data.history.map((h) => (
              <li key={h.day}>
                <Link href={`/quiz?day=${h.day}`} aria-current={h.day === data.day ? "page" : undefined}>{dayLabel(h.day)}</Link>
                <span className="faint">{h.answered} of {h.total} answered · {h.correct} right</span>
              </li>
            ))}
          </ul>
        </nav>
      )}
    </>
  );
}

function QuestionHead({ x, n }: { x: QuizQuestion; n: number }) {
  const kind = x.kind === "mcq" ? "Multiple choice" : x.kind === "subjective" ? "Write your answer" : "Answer out loud";
  return (
    <div className={styles.qHead}>
      <span className={styles.qNum}>{n}</span>
      <span className="chip">{TOPIC[x.topic]}</span>
      <span className="faint">{kind}</span>
      <p className={styles.prompt}>{x.prompt}</p>
      {x.source_url && (
        <a href={x.source_url} target="_blank" rel="noopener noreferrer" className={styles.source}>
          Source: {x.source_title || new URL(x.source_url).hostname} ↗
        </a>
      )}
    </div>
  );
}
