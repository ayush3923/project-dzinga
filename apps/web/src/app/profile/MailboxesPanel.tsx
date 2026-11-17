"use client";

import { useEffect, useState, useTransition } from "react";
import type { Lookback, Mailbox, SyncSettings } from "@/lib/api";
import { MailboxIcon } from "@/components/ui";
import {
  addMailboxAction, disconnectMailboxAction, microsoftStatusAction, saveMailboxPasswordAction,
  saveSyncSettingsAction, setMailboxEnabledAction, startMicrosoftAction, startSyncAction,
} from "../actions";
import styles from "./profile.module.css";

const LOOKBACK: { id: Lookback; label: string }[] = [
  { id: "since_last", label: "New since last sync" },
  { id: "14d", label: "Last 2 weeks" },
  { id: "1m", label: "Last month" },
  { id: "2m", label: "Last 2 months" },
  { id: "3m", label: "Last 3 months" },
  { id: "6m", label: "Last 6 months" },
  { id: "12m", label: "Last 12 months" },
  { id: "custom", label: "From a date…" },
];

const HELP: Record<string, { name: string; url: string }> = {
  yahoo: { name: "Yahoo", url: "https://login.yahoo.com/account/security" },
  gmail: { name: "Google", url: "https://myaccount.google.com/apppasswords" },
  icloud: { name: "Apple", url: "https://account.apple.com" },
};

const when = (iso: string | null) =>
  iso ? new Date(iso).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", timeZone: "Asia/Kolkata" }) : "never";

export function MailboxesPanel({ mailboxes, settings }: { mailboxes: Mailbox[]; settings: SyncSettings }) {
  const [lookback, setLookback] = useState<Lookback>(settings.lookback);
  const [sinceDate, setSinceDate] = useState(settings.since_date);
  const [address, setAddress] = useState("");
  const [pending, start] = useTransition();
  const [note, setNote] = useState<{ ok?: string; error?: string }>({});

  const run = (f: () => Promise<{ error?: string }>, ok?: string) =>
    start(async () => {
      const r = await f();
      setNote(r.error ? { error: r.error } : { ok });
    });

  const selected = mailboxes.filter((m) => m.enabled && m.credential_saved);

  return (
    <section className={`card ${styles.card}`} aria-labelledby="mb-h">
      <h2 id="mb-h">Connected mailboxes <span className="faint">read-only · credentials stay in your Mac’s Keychain</span></h2>

      <ul className={styles.mailboxes}>
        {mailboxes.map((m) => <MailboxRow key={m.id} m={m} />)}
        {mailboxes.length === 0 && <li className="muted">No mailboxes yet. Add one below.</li>}
      </ul>

      <div className={styles.addRow}>
        <label className="visually-hidden" htmlFor="mb-add">Add a mailbox</label>
        <input id="mb-add" className="input" type="email" placeholder="Add a Yahoo, Gmail, iCloud, Outlook or Live address"
          value={address} onChange={(e) => setAddress(e.target.value)} maxLength={254} />
        <button type="button" className="btn" disabled={pending || !address.includes("@")}
          onClick={() => run(async () => { const r = await addMailboxAction(address); if (!r.error) setAddress(""); return r; }, "Added. Now connect it.")}>
          Add
        </button>
      </div>

      <div className={styles.syncRow}>
        <label className={styles.field}>
          <span>How far back to scan</span>
          <select className="input" value={lookback} onChange={(e) => setLookback(e.target.value as Lookback)}>
            {LOOKBACK.map((l) => <option key={l.id} value={l.id}>{l.label}</option>)}
          </select>
        </label>
        {lookback === "custom" && (
          <label className={styles.field}>
            <span>From</span>
            <input className="input" type="date" value={sinceDate} max={new Date().toISOString().slice(0, 10)}
              onChange={(e) => setSinceDate(e.target.value)} />
          </label>
        )}
        <button type="button" className="btn" disabled={pending}
          onClick={() => run(() => saveSyncSettingsAction(lookback, sinceDate), "Saved.")}>Save</button>
        <span style={{ flex: 1 }} />
        <button type="button" className="btn btn-primary" disabled={pending || selected.length === 0}
          onClick={() => run(() => startSyncAction(selected.map((m) => m.id)), "Sync started. Progress shows in the sidebar.")}>
          Sync {selected.length} selected now
        </button>
      </div>
      <p className="faint">
        Re-scanning is safe: emails already imported are never duplicated. Sync also tidies grouping and stage history;
        paid AI analyses never run automatically.
      </p>
      {note.error && <p role="alert" className={styles.error}>{note.error}</p>}
      {note.ok && <p className="muted">{note.ok}</p>}
    </section>
  );
}

function MailboxRow({ m }: { m: Mailbox }) {
  const [mode, setMode] = useState<"idle" | "password" | "microsoft">("idle");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState<{ uri: string; code: string } | null>(null);
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();

  // Poll the Microsoft sign-in until it completes.
  useEffect(() => {
    if (mode !== "microsoft" || !code) return;
    const id = setInterval(async () => {
      const s = await microsoftStatusAction(m.id);
      if (s.status === "connected") { setMode("idle"); setCode(null); }
      if (s.status === "error") { setError(s.error); setMode("idle"); setCode(null); }
    }, 3000);
    return () => clearInterval(id);
  }, [mode, code, m.id]);

  const connect = () => {
    setError(undefined);
    if (m.provider !== "microsoft") { setMode("password"); return; }
    start(async () => {
      const r = await startMicrosoftAction(m.id);
      if (r.error) setError(r.error);
      else { setCode({ uri: r.uri!, code: r.code! }); setMode("microsoft"); }
    });
  };

  const savePassword = () =>
    start(async () => {
      const r = await saveMailboxPasswordAction(m.id, password);
      setPassword(""); // never keep it in the page
      if (r.error) setError(r.error);
      else setMode("idle");
    });

  const status = !m.credential_saved ? "Not connected" : m.last_status === "error" ? "Last sync failed" : "Connected";

  return (
    <li className={styles.mailbox} data-status={m.credential_saved ? m.last_status : "off"}>
      <div className={styles.mbMain}>
        <MailboxIcon account={m.address} />
        <span className={styles.mbAddr}>
          <strong>{m.address}</strong>
          <span className="faint">
            {status} · last sync {when(m.last_synced_at)}
            {m.last_synced_at && ` · ${m.last_added} new`} · {m.emails} job emails
          </span>
          {m.last_status === "error" && m.last_error && <span className={styles.error}>{m.last_error}</span>}
        </span>
        <label className={styles.include}>
          <input type="checkbox" checked={m.enabled} disabled={pending}
            onChange={(e) => start(async () => { const r = await setMailboxEnabledAction(m.id, e.target.checked); setError(r.error); })} />
          Include in sync
        </label>
      </div>

      <div className={styles.mbActions}>
        {mode === "idle" && (
          <>
            <button type="button" className="btn" onClick={connect} disabled={pending}>
              {m.credential_saved ? "Reconnect" : "Connect"}
            </button>
            {m.credential_saved && (
              <button type="button" className={styles.link} disabled={pending}
                onClick={() => start(async () => setError((await disconnectMailboxAction(m.id, false)).error))}>
                Disconnect
              </button>
            )}
            <button type="button" className={styles.link} disabled={pending}
              onClick={() => {
                if (confirm(`Remove ${m.address}? Its saved credential is deleted. Emails already imported stay.`)) {
                  start(async () => setError((await disconnectMailboxAction(m.id, true)).error));
                }
              }}>
              Remove
            </button>
          </>
        )}

        {mode === "password" && (
          <form className={styles.pwForm} onSubmit={(e) => { e.preventDefault(); savePassword(); }}>
            <label className={styles.field}>
              <span>
                App password for {m.address} ·{" "}
                <a href={HELP[m.provider]?.url} target="_blank" rel="noopener noreferrer">create one at {HELP[m.provider]?.name}</a>
              </span>
              <input className="input" type="password" autoComplete="off" value={password} maxLength={256}
                onChange={(e) => setPassword(e.target.value)} autoFocus />
            </label>
            <button type="submit" className="btn btn-primary" disabled={pending || !password.trim()}>
              {pending ? "Checking…" : "Check and save"}
            </button>
            <button type="button" className={styles.link} onClick={() => { setPassword(""); setMode("idle"); }}>Cancel</button>
          </form>
        )}

        {mode === "microsoft" && code && (
          <div className={styles.msBox}>
            <p>
              1. Open <a href={code.uri} target="_blank" rel="noopener noreferrer">{code.uri}</a> (a private window helps if
              another Microsoft account is signed in)
            </p>
            <p>2. Enter the code <strong className={styles.code}>{code.code}</strong></p>
            <p>3. Sign in as <strong>{m.address}</strong> and approve read-only mail access</p>
            <p className="faint">Waiting for approval…</p>
            <button type="button" className={styles.link} onClick={() => { setMode("idle"); setCode(null); }}>Cancel</button>
          </div>
        )}
      </div>
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </li>
  );
}
