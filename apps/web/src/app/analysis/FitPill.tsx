import styles from "./analysis.module.css";

export function FitPill({ total, large }: { total: number; large?: boolean }) {
  const band = total >= 75 ? "high" : total >= 55 ? "mid" : "low";
  return (
    <span className={large ? styles.pillLarge : styles.pill} data-band={band} title="Fit score out of 100">
      {total}
      {large && <small>/100</small>}
    </span>
  );
}
