// Thin client for the control plane's cookie-gated API.
//
// Every request carries the session cookie (credentials: "include") and speaks
// the D10 envelope: success bodies are { data: ... } and errors are
// { error: { code, message, fields? } }. The control plane sets an HttpOnly
// session cookie on login, so the browser attaches it automatically and the SPA
// never handles a token itself (DESIGN.md D5).
//
// CSRF protection for these cookie-authenticated, state-changing requests is the
// control plane's responsibility (D12 — do not reimplement auth), resting on the
// session cookie's SameSite attribute rather than a token minted here. This
// client deliberately adds no CSRF header; the dependency is that the control
// plane sets SameSite=Lax/Strict on the session cookie.

export const API_PREFIX = "/api/v1";

/** The error a failed API call throws, carrying the envelope's fields. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields?: Record<string, string[]>;

  constructor(status: number, code: string, message: string, fields?: Record<string, string[]>) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.fields = fields;
  }
}

interface ErrorEnvelope {
  error?: { code?: string; message?: string; fields?: Record<string, string[]> };
}

interface DataEnvelope<T> {
  data: T;
}

/**
 * request issues a call to the control plane and unwraps the { data } envelope.
 * A non-2xx response is thrown as an ApiError built from the { error } envelope.
 */
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(API_PREFIX + path, {
    ...init,
    credentials: "include",
    headers: {
      Accept: "application/json",
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });

  if (!resp.ok) {
    let code = "error";
    let message = resp.statusText || "request failed";
    let fields: Record<string, string[]> | undefined;
    try {
      const body = (await resp.json()) as ErrorEnvelope;
      if (body.error) {
        code = body.error.code ?? code;
        message = body.error.message ?? message;
        fields = body.error.fields;
      }
    } catch {
      // Non-JSON error body: keep the status text.
    }
    throw new ApiError(resp.status, code, message, fields);
  }

  if (resp.status === 204) {
    return undefined as T;
  }
  const body = (await resp.json()) as DataEnvelope<T>;
  return body.data;
}

// describeError turns an error into a message for the editor. For a validation
// failure it surfaces the field-keyed diagnostics (path: message) so the problem
// points at the offending entity rather than a generic "invalid" — the rest fall
// back to the envelope message.
export function describeError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.fields) {
      const parts = Object.entries(err.fields).flatMap(([key, msgs]) => msgs.map((m) => `${key}: ${m}`));
      if (parts.length > 0) {
        return parts.join("; ");
      }
    }
    return err.message;
  }
  return "Something went wrong";
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
  patch: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PATCH", body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body === undefined ? undefined : JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};
