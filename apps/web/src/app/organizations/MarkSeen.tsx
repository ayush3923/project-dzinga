"use client";

import { useEffect } from "react";
import { organizationSeenAction } from "@/app/actions";

/** Opening the drawer counts as checking what's new; this clears the list's flags. */
export function MarkSeen({ id }: { id: number }) {
  useEffect(() => {
    void organizationSeenAction(id);
  }, [id]);
  return null;
}
