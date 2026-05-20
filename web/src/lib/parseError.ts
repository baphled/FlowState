/**
 * parseError centralises the read-the-body / fall-back-to-statusText pattern
 * that every chatStore-touching API call had to repeat. The previous form was:
 *
 *   const err = await res.json().catch(() => ({ error: res.statusText })) as { error: string }
 *   throw new Error(err.error ?? `HTTP ${res.status}`)
 *
 * That cast lied — there is nothing constraining the backend to return
 * `{ error: string }`. A reverse proxy, a misconfigured load balancer, or a
 * future endpoint that returns `{ message: ... }` would all produce something
 * the cast asserts away rather than handles. parseError reads any JSON body,
 * accepts a few common shapes, and falls back to status text without lying
 * about types.
 *
 * Accepted shapes (best-effort):
 *   - `{ error: "msg" }`          (current FlowState convention)
 *   - `{ message: "msg" }`        (express/koa default)
 *   - `{ detail: "msg" }`         (FastAPI default)
 *   - `"msg"`                     (raw string body)
 *
 * Falls back to `${statusText} (HTTP ${status})` when no string field is
 * available or the body is not JSON. The returned message is always a
 * non-empty string suitable for `throw new Error(parseError(res))`.
 */
export async function parseError(res: Response): Promise<string> {
  const fallback = res.statusText
    ? `${res.statusText} (HTTP ${res.status})`
    : `HTTP ${res.status}`;

  let body: unknown;
  try {
    body = await res.json();
  } catch {
    return fallback;
  }

  if (typeof body === "string" && body.length > 0) {
    return body;
  }

  if (body && typeof body === "object") {
    const obj = body as Record<string, unknown>;
    for (const key of ["error", "message", "detail"] as const) {
      const value = obj[key];
      if (typeof value === "string" && value.length > 0) {
        return value;
      }
    }
  }

  return fallback;
}
