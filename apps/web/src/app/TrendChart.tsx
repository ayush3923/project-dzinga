"use client";

import { useEffect, useRef, useState } from "react";
import type { DayPoint } from "@/lib/api";
import styles from "./trend.module.css";

type Key = "applications" | "rejections" | "waiting";
const NAMES: Record<Key, string> = { applications: "Applied", rejections: "Rejected", waiting: "Waiting" };

const PAD = { top: 10, right: 74, bottom: 22, left: 26 };
const BASE_H = 116; // each panel's height; taller when filling its container

function niceMax(v: number) {
  if (v <= 4) return 4;
  const step = v <= 10 ? 2 : v <= 25 ? 5 : v <= 60 ? 10 : 20;
  return Math.ceil(v / step) * step;
}

function dayLabel(date: string, short = true) {
  const d = new Date(`${date}T00:00:00+05:30`);
  return d.toLocaleDateString("en-IN", short ? { weekday: "short" } : { weekday: "short", day: "numeric", month: "short" });
}

/**
 * Two small multiples on one shared day axis: daily flows (applied, rejected)
 * and the stock of applications still waiting for a reply. They have different
 * scales, so they never share a y-axis. Hovering a day shows all three values.
 */
export function TrendChart({ series, fill }: { series: DayPoint[]; fill?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(360);
  const [H, setH] = useState(BASE_H);
  const [hover, setHover] = useState<number | null>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.max(260, Math.floor(e.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // With fill, the two panels share whatever height the container has (it is
  // stretched to the column beside it); everything else keeps its natural size.
  useEffect(() => {
    const el = ref.current;
    const box = el?.parentElement;
    if (!fill || !el || !box) return;
    const fit = () => {
      const svgs = el.querySelectorAll("svg");
      const charts = Array.from(svgs).reduce((n, s) => n + s.getBoundingClientRect().height, 0);
      const other = el.scrollHeight - charts;
      setH(Math.max(BASE_H, Math.floor((box.clientHeight - other) / 2)));
    };
    const ro = new ResizeObserver(fit);
    ro.observe(box);
    return () => ro.disconnect();
  }, [fill]);

  const n = series.length;
  const innerW = w - PAD.left - PAD.right;
  const x = (i: number) => PAD.left + (n <= 1 ? innerW / 2 : (i * innerW) / (n - 1));

  const panel = (keys: Key[], title: string, unit: string) => {
    const max = niceMax(Math.max(1, ...series.flatMap((p) => keys.map((k) => p[k]))));
    const innerH = H - PAD.top - PAD.bottom;
    const y = (v: number) => PAD.top + innerH - (v / max) * innerH;
    const last = series[n - 1];
    // End labels: nudge apart only when the two would collide.
    const ends = keys.map((k) => ({ k, y: y(last[k]) }));
    if (ends.length === 2 && Math.abs(ends[0].y - ends[1].y) < 18) {
      const mid = Math.min((ends[0].y + ends[1].y) / 2, H - PAD.bottom - 12); // keep clear of the day labels
      const up = ends[0].y <= ends[1].y ? 0 : 1;
      ends[up].y = mid - 9;
      ends[1 - up].y = mid + 9;
    }
    return (
      <figure className={styles.panel}>
        <figcaption className={styles.caption}>
          <span>{title}</span>
          <span className={styles.keys}>
            {keys.map((k) => (
              <span key={k} className={styles.key}><i data-k={k} />{NAMES[k]}</span>
            ))}
          </span>
        </figcaption>
        <svg width={w} height={H} role="img" aria-label={`${title}, last 7 days`} className={styles.svg}
          onMouseLeave={() => setHover(null)}>
          {[0, max / 2, max].map((t) => (
            <g key={t}>
              <line x1={PAD.left} x2={w - PAD.right} y1={y(t)} y2={y(t)} className={styles.grid} />
              <text x={PAD.left - 6} y={y(t)} className={styles.tick} textAnchor="end" dominantBaseline="middle">{t}</text>
            </g>
          ))}
          {series.map((p, i) => (
            <text key={p.date} x={x(i)} y={H - 6} className={styles.tick} textAnchor="middle">{dayLabel(p.date)}</text>
          ))}
          {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={H - PAD.bottom} className={styles.cross} />}
          {keys.map((k) => (
            <g key={k} data-k={k} className={styles.series}>
              <polyline points={series.map((p, i) => `${x(i)},${y(p[k])}`).join(" ")} className={styles.line} />
              {series.map((p, i) => (
                <circle key={p.date} cx={x(i)} cy={y(p[k])} r={hover === i ? 5 : 3.5} className={styles.dot} />
              ))}
            </g>
          ))}
          {ends.map(({ k, y: ey }) => (
            <text key={k} x={x(n - 1) + 9} y={ey} className={styles.end} dominantBaseline="middle">
              {NAMES[k]} <tspan className={styles.endValue}>{last[k]}</tspan>
            </text>
          ))}
          {series.map((p, i) => (
            <rect key={p.date} x={x(i) - innerW / (2 * Math.max(1, n - 1))} y={0} width={innerW / Math.max(1, n - 1)} height={H}
              fill="transparent" onMouseEnter={() => setHover(i)} onFocus={() => setHover(i)} />
          ))}
        </svg>
        <span className="visually-hidden">{unit}</span>
      </figure>
    );
  };

  return (
    <div ref={ref} className={styles.root}>
      {panel(["applications", "rejections"], "Sent and rejected, per day", "Applications sent and rejections received each day (IST).")}
      {panel(["waiting"], "Waiting for a reply", "Applications from the previous 45 days with no reply yet, at the end of each day.")}
      {hover !== null && (
        <div className={styles.tip} style={{ left: Math.min(Math.max(x(hover) - 70, 0), w - 150) }} role="status">
          <strong>{dayLabel(series[hover].date, false)}</strong>
          {(["applications", "rejections", "waiting"] as Key[]).map((k) => (
            <span key={k}><i data-k={k} />{NAMES[k]} <b>{series[hover][k]}</b></span>
          ))}
        </div>
      )}
      <div className="visually-hidden">
        <table>
          <caption>Last 7 days, by day</caption>
          <thead><tr><th>Day</th><th>Applied</th><th>Rejected</th><th>Waiting</th></tr></thead>
          <tbody>
            {series.map((p) => (
              <tr key={p.date}><td>{dayLabel(p.date, false)}</td><td>{p.applications}</td><td>{p.rejections}</td><td>{p.waiting}</td></tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
