"use client";

import { useEffect } from "react";
import { briefingSeenAction } from "@/app/actions";

/** Opening the drawer counts as checking what's new; this clears the list's flag. */
export function MarkSeen({ id }: { id: number }) {
  useEffect(() => {
    void briefingSeenAction(id);
  }, [id]);
  return null;
}
