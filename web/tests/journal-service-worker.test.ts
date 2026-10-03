// @vitest-environment node
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { describe, expect, it, vi } from "vitest";

const origin = "http://127.0.0.1:8081";
const worker = readFileSync(new URL("../public/journal-sw.js", import.meta.url), "utf8");
type CacheInput = string | URL | Request;
type FetchEvent = { request: Request; respondWith: (response: Promise<Response>) => void };

function makeWorker() {
  const entries = new Map<string, { request: Request; response: Response }>();
  const toRequest = (input: CacheInput) => (input instanceof Request ? input : new Request(new URL(String(input), origin)));
  const cache = {
    async put(input: CacheInput, response: Response) {
      const request = toRequest(input);
      entries.set(request.url, { request, response: response.clone() });
    },
    async match(input: CacheInput, options?: { ignoreVary?: boolean }) {
      const request = toRequest(input);
      const entry = entries.get(request.url);
      if (!entry) return undefined;
      if (!options?.ignoreVary) {
        const varied = entry.response.headers.get("vary")?.split(",") ?? [];
        if (
          varied.some((header) => header.trim() === "*" || entry.request.headers.get(header.trim()) !== request.headers.get(header.trim()))
        ) {
          return undefined;
        }
      }
      return entry.response.clone();
    },
  };
  const network = vi.fn().mockRejectedValue(new TypeError("Server is offline"));
  const openCache = vi.fn().mockResolvedValue(cache);
  let onFetch: ((event: FetchEvent) => void) | undefined;
  runInNewContext(worker, {
    URL,
    Response,
    fetch: network,
    caches: { open: openCache },
    self: {
      location: { origin },
      addEventListener(type: string, listener: (event: FetchEvent) => void) {
        if (type === "fetch") onFetch = listener;
      },
    },
  });
  return {
    cache,
    network,
    openCache,
    dispatch(request: Request) {
      const respondWith = vi.fn<(response: Promise<Response>) => void>();
      onFetch?.({ request, respondWith });
      return respondWith;
    },
  };
}

describe("offline journal service worker", () => {
  it.each(["open", "match", "put"])("keeps an online module available when cache %s fails", async (operation) => {
    const { cache, dispatch, network, openCache } = makeWorker();
    const failure = new DOMException("Cache unavailable", "QuotaExceededError");
    if (operation === "open") openCache.mockRejectedValue(failure);
    else vi.spyOn(cache, operation as "match" | "put").mockRejectedValue(failure);
    network.mockResolvedValue(new Response("export default 'available'", { headers: { "Content-Type": "text/javascript" } }));
    const request = new Request(`${origin}/assets/route.js`, { mode: "cors" });
    const response = await dispatch(request).mock.calls[0]?.[0];
    expect(response?.status).toBe(200);
    expect(await response?.text()).toBe("export default 'available'");
    expect(network).toHaveBeenCalledExactlyOnceWith(request);
  });

  it("returns fresh online HTML when its cache write fails instead of falling back to an old build", async () => {
    const { cache, dispatch, network } = makeWorker();
    await cache.put("/", new Response("old build"));
    vi.spyOn(cache, "put").mockRejectedValue(new DOMException("Storage full", "QuotaExceededError"));
    network.mockResolvedValue(new Response("new build", { headers: { "Content-Type": "text/html" } }));
    const request = new Request(`${origin}/`);
    Object.defineProperty(request, "mode", { value: "navigate" });
    const response = await dispatch(request).mock.calls[0]?.[0];
    expect(await response?.text()).toBe("new build");
    expect(network).toHaveBeenCalledOnce();
  });

  it("does not store a missing asset response", async () => {
    const { cache, dispatch, network } = makeWorker();
    const put = vi.spyOn(cache, "put");
    network.mockResolvedValue(new Response("Not found", { status: 404 }));
    const response = await dispatch(new Request(`${origin}/assets/missing.js`)).mock.calls[0]?.[0];
    expect(response?.status).toBe(404);
    expect(put).not.toHaveBeenCalled();
  });

  it.each([
    "/assets/index-test.js",
    "/assets/index-test.css",
  ])("serves warmed %s to a crossorigin resource request when the server is stopped", async (path) => {
    const { cache, dispatch, network } = makeWorker();
    // The server's CORS middleware emits Vary: Origin even for immutable
    // assets. Warm-up fetches omit Origin; real modulepreload requests add it.
    await cache.put(path, new Response("static application asset", { headers: { Vary: "Origin" } }));
    const request = new Request(`${origin}${path}`, { headers: { Origin: origin }, mode: "cors" });
    expect(request.headers.get("origin")).toBe(origin);
    expect(await cache.match(request)).toBeUndefined();
    const response = await dispatch(request).mock.calls[0]?.[0];
    expect(await response?.text()).toBe("static application asset");
    expect(network).not.toHaveBeenCalled();
  });

  it.each([
    "/api/v1/journal/shares",
    "/api/v1/journal-shares/secret",
    "/file/attachments/private",
    "/s/secret",
    "/memos/shares/secret",
    "/memos/record?share_token=secret",
    "/assets/index-test.js?share_token=secret",
  ])("never intercepts or caches the private/public-share endpoint %s", (path) => {
    const { dispatch, network } = makeWorker();
    expect(dispatch(new Request(`${origin}${path}`))).not.toHaveBeenCalled();
    expect(network).not.toHaveBeenCalled();
  });

  it("does not widen the static resource exception to another origin or a mutation", () => {
    const { dispatch } = makeWorker();
    expect(dispatch(new Request("https://example.test/assets/index.js"))).not.toHaveBeenCalled();
    expect(dispatch(new Request(`${origin}/assets/index.js`, { method: "POST" }))).not.toHaveBeenCalled();
  });
});
