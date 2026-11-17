import { api } from "@/lib/api";

/** Plays back a recorded quiz answer. Recordings stay on this machine (core-api's disk). */
export async function GET(_req: Request, ctx: RouteContext<"/quiz/audio/[id]">) {
  const { id } = await ctx.params;
  if (!/^\d{1,12}$/.test(id)) return new Response("Not found", { status: 404 });
  try {
    const res = await api.voice(Number(id));
    return new Response(res.body, {
      headers: {
        "Content-Type": res.headers.get("Content-Type") ?? "application/octet-stream",
        "Cache-Control": "private, no-store",
        "X-Content-Type-Options": "nosniff",
      },
    });
  } catch {
    return new Response("Not found", { status: 404 });
  }
}
