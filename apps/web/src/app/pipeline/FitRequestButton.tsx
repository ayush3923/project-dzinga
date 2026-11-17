"use client";

import { useState, useTransition } from "react";
import { requestFitAction } from "@/app/actions";

/** Queues an Opus fit score; Claude's scheduled run picks it up. */
export function FitRequestButton({ id }: { id: number }) {
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  return (
    <>
      <button type="button" className="link" disabled={pending}
        onClick={() => start(async () => setError((await requestFitAction(id)).error))}>
        {pending ? "Queuing…" : "Queue a fit score"}
      </button>
      {error && <p role="alert" style={{ color: "var(--accent)", fontSize: 13, margin: 0 }}>{error}</p>}
    </>
  );
}
