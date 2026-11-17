// Kubernetes probes hit this: it touches no other service, so a core-api outage doesn't restart the web pod.
export function GET() {
  return new Response("ok");
}
