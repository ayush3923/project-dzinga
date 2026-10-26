"use client";

import { useState, useTransition } from "react";
import { requestResearchAction } from "@/app/actions";

/**
 * Web research runs in the Claude app (not through the API, to save cost): this
 * queues it, and Claude works through the queue when asked.
 */
export function ResearchButton({ kind, id, label, requestedAt, quiet }: {
  kind: "organizations" | "applications";
  id: number;
  label: string;
  requestedAt: string | null;
  quiet?: boolean;
}) {
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  if (requestedAt) {
    return (
      <p className="faint" style={{ margin: 0 }}>
        Queued for Claude {new Date(requestedAt).toLocaleDateString("en-IN", { day: "numeric", month: "short" })}. In the Claude app, ask:
        {" "}<q>do the pending Dzinga research</q>.
      </p>
    );
  }
  return (
    <>
      <button type="button" className={quiet ? "link" : "btn btn-primary"} disabled={pending}
        onClick={() => start(async () => setError((await requestResearchAction(kind, id)).error))}>
        {pending ? "Queuing…" : label}
      </button>
      {error && <p role="alert" style={{ color: "var(--accent)", fontSize: 13, margin: 0 }}>{error}</p>}
    </>
  );
}
