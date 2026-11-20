/**
 * Readiness index (0–100): how ready the search is today, from three equal parts.
 *  - Quiz: the last 7 days' quiz performance. MCQ accuracy (40%), how many of the 7
 *    days had an answer (30%, so skipping days counts against it) and how well the
 *    improvement drills from earlier feedback were followed (30%). Until a drill has
 *    been checked, accuracy and days count half each.
 *  - Apply momentum: applications in the last 7 days against a weekly target.
 *  - Interviews: live processes (interview or assessment stage); 3 or more is full.
 */

export const WEEKLY_APPLY_TARGET = 10;
export const FULL_INTERVIEWS = 3;

export interface ReadinessInput {
  /** MCQ accuracy over the last 7 days, 0–100 (null when nothing was answered). */
  accuracy7d: number | null;
  /** Quiz days with how many questions were answered, any range (only the last 7 count). */
  quizDays: { day: string; answered: number }[];
  /** How well improvement drills checked in the last 7 days were followed, 0–100 (null when none were checked). */
  followThrough7d: number | null;
  /** Applications sent in the last 7 days. */
  applied7d: number;
  /** Applications in the interview or assessment stage now. */
  activeInterviews: number;
  /** Today's date, YYYY-MM-DD, in the user's time zone. */
  today: string;
}

export interface Readiness {
  index: number;
  quiz: number;
  momentum: number;
  interviews: number;
  detail: { accuracy7d: number | null; quizDays7d: number; followThrough7d: number | null; applied7d: number; activeInterviews: number };
}

const clamp = (v: number) => Math.max(0, Math.min(100, v));

function daysBefore(day: string, n: number): string {
  const d = new Date(`${day}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() - n);
  return d.toISOString().slice(0, 10);
}

export function readiness(r: ReadinessInput): Readiness {
  const from = daysBefore(r.today, 6);
  const quizDays7d = new Set(r.quizDays.filter((d) => d.answered > 0 && d.day >= from && d.day <= r.today).map((d) => d.day)).size;
  const accuracy = r.accuracy7d ?? 0;
  const days = (quizDays7d / 7) * 100;
  const quiz = Math.round(r.followThrough7d === null ? (accuracy + days) / 2 : 0.4 * accuracy + 0.3 * days + 0.3 * r.followThrough7d);
  const momentum = Math.round(clamp((r.applied7d / WEEKLY_APPLY_TARGET) * 100));
  const interviews = Math.round(clamp((r.activeInterviews / FULL_INTERVIEWS) * 100));
  return {
    index: Math.round((quiz + momentum + interviews) / 3),
    quiz,
    momentum,
    interviews,
    detail: { accuracy7d: r.accuracy7d, quizDays7d, followThrough7d: r.followThrough7d, applied7d: r.applied7d, activeInterviews: r.activeInterviews },
  };
}
