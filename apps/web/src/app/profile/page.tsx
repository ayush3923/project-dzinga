import { Suspense } from "react";
import { connection } from "next/server";
import { api, type Mailbox, type NewsItem, type Profile, type QuizStats, type SyncSettings } from "@/lib/api";
import { Offline } from "@/components/ui";
import { ProfileForm } from "./ProfileForm";
import { MailboxesPanel } from "./MailboxesPanel";
import { CareerHistory, KeyNews, MailSummary, ProfileHero, QuizPerformance } from "./ProfileHighlights";
import { Disclosure } from "./Disclosure";
import styles from "./profile.module.css";

export default function ProfilePage() {
  return (
    <div className={styles.page}>
      <header>
        <p className="eyebrow">Your profile</p>
        <h1 className="display">What you bring</h1>
        <p className="muted">Fit scores and cold emails use only what’s written here.</p>
      </header>
      <Suspense fallback={<div className="skeleton" style={{ height: 420 }} />}>
        <Loaded />
      </Suspense>
    </div>
  );
}

async function Loaded() {
  await connection(); // the current role's duration runs to today: render per request, before any fetch
  let profile: Profile;
  let boxes: { mailboxes: Mailbox[]; settings: SyncSettings };
  // The quiz service is optional here: the profile still loads without it.
  const [stats, news] = await Promise.all([
    api.quizStats().catch((): QuizStats | null => null),
    api.news().catch((): { day: string; items: NewsItem[] } | null => null),
  ]);
  try {
    [profile, boxes] = await Promise.all([api.profile(), api.mailboxes()]);
  } catch {
    return <Offline />;
  }
  const now = new Date();
  return (
    <>
      <div className={styles.board}>
        <ProfileHero p={profile} />
        <QuizPerformance s={stats} />
        <CareerHistory experiences={profile.experiences} now={now} />
        <KeyNews news={news} />
        <MailSummary mailboxes={boxes.mailboxes} />
      </div>
      <Disclosure id="edit-profile" title="Edit profile" hint="Basics, skills and roles used for fit scores and cold emails">
        <ProfileForm initial={profile} />
      </Disclosure>
      <Disclosure id="mailboxes" title="Manage mailboxes" hint="Connect, pause or re-scan · credentials stay in your Mac’s Keychain">
        <MailboxesPanel mailboxes={boxes.mailboxes} settings={boxes.settings} />
      </Disclosure>
    </>
  );
}
