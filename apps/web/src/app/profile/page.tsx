import { Suspense } from "react";
import { api, type Mailbox, type Profile, type SyncSettings } from "@/lib/api";
import { Offline } from "@/components/ui";
import { ProfileForm } from "./ProfileForm";
import { MailboxesPanel } from "./MailboxesPanel";
import styles from "./profile.module.css";

export default function ProfilePage() {
  return (
    <div className={styles.page}>
      <header>
        <p className="eyebrow">Your profile</p>
        <h1 className="display">What you bring</h1>
        <p className="muted">
          Your mailboxes, and what you bring. Fit scores and cold emails use only what’s written here.
        </p>
      </header>
      <Suspense fallback={<div className="skeleton" style={{ height: 420 }} />}>
        <Loaded />
      </Suspense>
    </div>
  );
}

async function Loaded() {
  let profile: Profile;
  let boxes: { mailboxes: Mailbox[]; settings: SyncSettings };
  try {
    [profile, boxes] = await Promise.all([api.profile(), api.mailboxes()]);
  } catch {
    return <Offline />;
  }
  return (
    <>
      <MailboxesPanel mailboxes={boxes.mailboxes} settings={boxes.settings} />
      <ProfileForm initial={profile} />
    </>
  );
}
