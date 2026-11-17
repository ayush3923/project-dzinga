"use client";

import { useEffect, useRef, useState, useTransition } from "react";
import { answerQuizAction, uploadVoiceAction } from "@/app/actions";
import type { QuizQuestion } from "@/lib/api";
import styles from "./quiz.module.css";

function Feedback({ q }: { q: QuizQuestion }) {
  const r = q.response;
  if (!r) return null;
  if (!r.feedback_at) {
    return <p className="faint">Saved. Claude adds feedback on its next scheduled run.</p>;
  }
  return (
    <div className={styles.feedback}>
      <strong>Feedback{r.score !== null && <> · {r.score}/10</>}</strong>
      <p>{r.feedback}</p>
      {r.transcript && (
        <details>
          <summary>What Claude heard</summary>
          <p className="muted">{r.transcript}</p>
        </details>
      )}
    </div>
  );
}

/** Pick an option; the answer and explanation are revealed once chosen. */
export function McqQuestion({ q: initial }: { q: QuizQuestion }) {
  const [q, setQ] = useState(initial);
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  const done = q.response !== null;
  const choose = (i: number) =>
    start(async () => {
      const res = await answerQuizAction(q.id, i);
      if (res.question) setQ(res.question);
      setError(res.error);
    });
  return (
    <div className={styles.answer}>
      <ul className={styles.options} role="list">
        {q.options.map((o, i) => {
          const state = !done ? undefined : i === q.answer ? "right" : i === q.response?.choice ? "wrong" : undefined;
          return (
            <li key={i}>
              <button type="button" className={styles.option} data-state={state} disabled={done || pending}
                aria-pressed={q.response?.choice === i} onClick={() => choose(i)}>
                <span className={styles.letter}>{String.fromCharCode(65 + i)}</span> {o}
              </button>
            </li>
          );
        })}
      </ul>
      {done && (
        <p className={styles.result} data-correct={q.response?.correct ?? false}>
          <strong>{q.response?.correct ? "Right." : "Not quite."}</strong> {q.explanation}
        </p>
      )}
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </div>
  );
}

/** The written question. It can be revised until Claude has given feedback. */
export function WrittenQuestion({ q: initial }: { q: QuizQuestion }) {
  const [q, setQ] = useState(initial);
  const [text, setText] = useState(initial.response?.text ?? "");
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  const locked = !!q.response?.feedback_at;
  return (
    <form className={styles.answer} onSubmit={(e) => {
      e.preventDefault();
      start(async () => {
        const res = await answerQuizAction(q.id, text);
        if (res.question) setQ(res.question);
        setError(res.error);
      });
    }}>
      <label className="visually-hidden" htmlFor={`a${q.id}`}>Your answer</label>
      <textarea id={`a${q.id}`} className={styles.textarea} rows={6} maxLength={4000} value={text} disabled={locked}
        onChange={(e) => setText(e.target.value)} placeholder="A few short paragraphs is plenty." />
      {!locked && (
        <button type="submit" className="btn btn-primary" disabled={pending || text.trim() === ""}>
          {pending ? "Saving…" : q.response ? "Update answer" : "Submit answer"}
        </button>
      )}
      {error && <p role="alert" className={styles.error}>{error}</p>}
      <Feedback q={q} />
    </form>
  );
}

const MAX_SECONDS = 180;

function pickType(): string {
  for (const t of ["audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus", "audio/mp4"]) {
    if (typeof MediaRecorder !== "undefined" && MediaRecorder.isTypeSupported(t)) return t;
  }
  return "";
}

/** The spoken question: record in the browser, replay, then save. The file stays on this Mac. */
export function VoiceQuestion({ q: initial }: { q: QuizQuestion }) {
  const [q, setQ] = useState(initial);
  const [recording, setRecording] = useState(false);
  const [seconds, setSeconds] = useState(0);
  const [clip, setClip] = useState<{ blob: Blob; url: string } | null>(null);
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  const rec = useRef<MediaRecorder | null>(null);
  const timer = useRef<ReturnType<typeof setInterval> | null>(null);
  const locked = !!q.response?.feedback_at;

  useEffect(() => () => {
    if (timer.current) clearInterval(timer.current);
    rec.current?.stream.getTracks().forEach((t) => t.stop());
  }, []);
  useEffect(() => () => { if (clip) URL.revokeObjectURL(clip.url); }, [clip]);

  const stop = () => {
    if (timer.current) clearInterval(timer.current);
    rec.current?.stop();
    setRecording(false);
  };
  const begin = async () => {
    setError(undefined);
    const type = pickType();
    if (!type || !navigator.mediaDevices) {
      setError("This browser can’t record audio here. Try Chrome or Safari.");
      return;
    }
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch {
      setError("Microphone access was blocked. Allow it for this site and try again.");
      return;
    }
    const chunks: Blob[] = [];
    const r = new MediaRecorder(stream, { mimeType: type });
    r.ondataavailable = (e) => { if (e.data.size) chunks.push(e.data); };
    r.onstop = () => {
      stream.getTracks().forEach((t) => t.stop());
      const blob = new Blob(chunks, { type: type.split(";")[0] });
      setClip({ blob, url: URL.createObjectURL(blob) });
    };
    rec.current = r;
    r.start(1000);
    setClip(null);
    setSeconds(0);
    setRecording(true);
    timer.current = setInterval(() => setSeconds((s) => {
      if (s + 1 >= MAX_SECONDS) stop();
      return s + 1;
    }), 1000);
  };
  const save = () =>
    start(async () => {
      if (!clip) return;
      const form = new FormData();
      form.set("id", String(q.id));
      form.set("audio", clip.blob, "answer");
      const res = await uploadVoiceAction(form);
      if (res.question) {
        setQ(res.question);
        setClip(null);
      }
      setError(res.error);
    });

  const mmss = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
  return (
    <div className={styles.answer}>
      {!locked && (
        <div className={styles.recorder}>
          {recording ? (
            <button type="button" className="btn btn-primary" onClick={stop}>■ Stop · {mmss}</button>
          ) : (
            <button type="button" className="btn" onClick={begin} disabled={pending}>
              ● {q.response || clip ? "Record again" : "Record answer"}
            </button>
          )}
          <span className="faint">Up to {MAX_SECONDS / 60} minutes. Saved only on this Mac.</span>
        </div>
      )}
      {clip && (
        <div className={styles.recorder}>
          <audio controls src={clip.url} />
          <button type="button" className="btn btn-primary" onClick={save} disabled={pending}>
            {pending ? "Saving…" : "Save recording"}
          </button>
        </div>
      )}
      {!clip && q.response?.audio_file && (
        <audio controls preload="none" src={`/quiz/audio/${q.id}?v=${encodeURIComponent(q.response.audio_file)}`} />
      )}
      {error && <p role="alert" className={styles.error}>{error}</p>}
      <Feedback q={q} />
    </div>
  );
}
