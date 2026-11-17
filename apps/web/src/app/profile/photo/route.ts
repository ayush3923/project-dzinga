import { api } from "@/lib/api";

/** The profile photo, served from core-api. It never leaves this machine. */
export async function GET() {
  try {
    const res = await api.photo();
    return new Response(res.body, {
      headers: { "Content-Type": "image/jpeg", "Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff" },
    });
  } catch {
    return new Response("Not found", { status: 404 });
  }
}
