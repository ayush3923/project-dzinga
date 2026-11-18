import type { FitBand } from "@/lib/api";
import styles from "./analysis.module.css";

const bandName: Record<FitBand, string> = { strong: "Strong", good: "Good", moderate: "Moderate", weak: "Weak" };

export function FitPill({ total, band, large }: { total: number; band: FitBand; large?: boolean }) {
  return (
    <span className={large ? styles.pillLarge : styles.pill} data-band={band} title={`${bandName[band]} fit, out of 100`}>
      {total}
      {large && <small>/100</small>}
    </span>
  );
}
