"use client";

import { useRef, useState, useTransition } from "react";
import { deletePhotoAction, uploadPhotoAction } from "@/app/actions";
import styles from "./profile.module.css";

const SIDE = 512;

/** Shrinks the photo in the browser (respecting its rotation) to at most 512 px, as JPEG. */
async function shrink(file: File): Promise<Blob> {
  const bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  const scale = Math.min(1, SIDE / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(bitmap.width * scale));
  canvas.height = Math.max(1, Math.round(bitmap.height * scale));
  canvas.getContext("2d")!.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  return new Promise((resolve, reject) =>
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("encode"))), "image/jpeg", 0.9));
}

/** The profile photo with upload, change and remove. Initials stand in when there's none. */
export function PhotoControl({ name, photoAt }: { name: string; photoAt: string | null }) {
  const input = useRef<HTMLInputElement>(null);
  const [pending, start] = useTransition();
  const [error, setError] = useState<string>();
  const initials = name.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]?.toUpperCase()).join("") || "?";

  const pick = (file: File | undefined) => {
    if (!file) return;
    setError(undefined);
    if (!/^image\/(jpeg|png)$/.test(file.type)) {
      setError("Choose a JPEG or PNG photo.");
      return;
    }
    start(async () => {
      try {
        const form = new FormData();
        form.set("photo", await shrink(file), "photo.jpg");
        setError((await uploadPhotoAction(form)).error);
      } catch {
        setError("Couldn’t read that image. Try another photo.");
      }
    });
  };

  return (
    <div className={styles.photoBox}>
      <div className={styles.photo} aria-busy={pending}>
        {photoAt
          // eslint-disable-next-line @next/next/no-img-element -- a private, per-user image served by this app
          ? <img src={`/profile/photo?v=${encodeURIComponent(photoAt)}`} alt={`Photo of ${name || "you"}`} width={96} height={96} />
          : <span aria-hidden="true">{initials}</span>}
      </div>
      <div className={styles.photoActions}>
        <button type="button" className={styles.link} disabled={pending} onClick={() => input.current?.click()}>
          {pending ? "Saving…" : photoAt ? "Change photo" : "Upload photo"}
        </button>
        {photoAt && !pending && (
          <button type="button" className={styles.link} onClick={() => start(async () => setError((await deletePhotoAction()).error))}>
            Remove
          </button>
        )}
      </div>
      <input ref={input} type="file" accept="image/jpeg,image/png" hidden
        onChange={(e) => { pick(e.target.files?.[0]); e.target.value = ""; }} />
      {error && <p role="alert" className={styles.error}>{error}</p>}
    </div>
  );
}
