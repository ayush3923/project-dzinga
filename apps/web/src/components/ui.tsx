import { STAGE_META, initials, providerOf } from "@/lib/format";
import type { EventKind } from "@/lib/api";
import styles from "./ui.module.css";

export function StageBadge({ stage }: { stage: EventKind }) {
  return (
    <span className="badge" data-stage={stage}>
      <span className="dot" />
      {STAGE_META[stage].label}
    </span>
  );
}

export function CompanyLogo({ company, stage }: { company: string; stage?: string }) {
  return (
    <span className="logo" data-stage={stage} aria-hidden="true">
      {initials(company)}
    </span>
  );
}

/** Small badge showing which mailbox an email came from; full address on hover. */
export function MailboxIcon({ account }: { account: string }) {
  const p = providerOf(account);
  return (
    <span className={styles.mailbox} data-provider={p.id} title={`${p.label}: ${account}`} role="img"
      aria-label={`From ${p.label} (${account})`}>
      {p.letter}
    </span>
  );
}

export function MailboxIcons({ accounts }: { accounts: string[] }) {
  if (accounts.length === 0) return null;
  return (
    <span className={styles.mailboxes}>
      {accounts.map((a) => <MailboxIcon key={a} account={a} />)}
    </span>
  );
}

export function Offline() {
  return (
    <div className={`card ${styles.notice}`} role="alert">
      <p className="eyebrow">Can’t reach your data</p>
      <h2 className="display">The core service isn’t running.</h2>
      <p className="muted">Start it, then refresh this page:</p>
      <pre className={styles.code}>make core-api</pre>
    </div>
  );
}

export function EmptyInbox() {
  return (
    <div className={`card ${styles.notice}`}>
      <p className="eyebrow">Nothing here yet</p>
      <h2 className="display">Let’s bring in your applications.</h2>
      <p className="muted">
        Run the importer in a terminal. It reads your mailbox without changing anything and asks for your
        app password privately.
      </p>
      <pre className={styles.code}>make ingest-yahoo EMAIL=you@yahoo.com</pre>
      <p className="faint">Want to look around first? Load the demo mailbox:</p>
      <pre className={styles.code}>make demo</pre>
    </div>
  );
}
