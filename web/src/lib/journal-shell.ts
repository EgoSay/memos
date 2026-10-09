/** Warm only the built application assets; never persist API responses or tokens. */
export async function registerJournalShell() {
  if (!import.meta.env.PROD || !("serviceWorker" in navigator)) return;
  try {
    await navigator.serviceWorker.register("/journal-sw.js", { scope: "/" });
    const registration = await navigator.serviceWorker.ready;
    const urls = performance.getEntriesByType("resource").map((entry) => entry.name);
    registration.active?.postMessage({ type: "journal-cache-static", urls });
  } catch {
    // An unsupported/blocked worker must not prevent online writing. Durable
    // draft status is reported by the editor independently.
  }
}
