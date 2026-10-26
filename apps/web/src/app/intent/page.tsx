import { Suspense } from "react";
import { api } from "@/lib/api";
import { Offline } from "@/components/ui";
import { IntentWizard } from "./IntentWizard";
import styles from "./intent.module.css";

export default function IntentPage() {
  return (
    <div className={styles.page}>
      <header>
        <p className="eyebrow">Stage 1 · Candidate intent</p>
        <h1 className="display">What are you looking for?</h1>
        <p className="muted">
          This shapes everything else: which jobs Dzinga finds, how it ranks them, and what it tells you each morning.
        </p>
      </header>
      <Suspense fallback={<div className="skeleton" style={{ height: 360 }} />}>
        <Loaded />
      </Suspense>
    </div>
  );
}

async function Loaded() {
  let intent;
  try {
    intent = await api.intent();
  } catch {
    return <Offline />;
  }
  return <IntentWizard initial={intent} />;
}
