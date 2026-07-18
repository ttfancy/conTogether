const API_KEY_STORAGE_KEY = 'contogether_api_key'

// A plain module-level accessor, not React state: the API client has no
// business depending on React, and every request (REST or WebSocket)
// needs the current key regardless of which component triggered it.
// ApiKeyContext is the thing that keeps components in sync with this.
export function getApiKey(): string {
  return localStorage.getItem(API_KEY_STORAGE_KEY) ?? ''
}

export function setStoredApiKey(key: string): void {
  localStorage.setItem(API_KEY_STORAGE_KEY, key)
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

// missingApiKeyError is checked before ever touching the network — every
// request() and the two raw-fetch upload helpers (uploadFile,
// downloadUpload) call this, so "no API key set" surfaces the same clear
// message everywhere exactly once, instead of each page independently
// discovering it via a generic 401 (or, worse, some pages not checking
// at all).
export function missingApiKeyError(): ApiError | null {
  return getApiKey().trim() ? null : new ApiError(401, 'Enter an API key (top right) first.')
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const missing = missingApiKeyError()
  if (missing) throw missing

  const headers = new Headers(init.headers)
  headers.set('X-API-Key', getApiKey())
  // Explicit, not just relying on fetch()'s default `Accept: */*`: the
  // dev proxy (vite.config.ts) distinguishes a real page navigation
  // (Accept: text/html) from an API call specifically so it can bypass
  // proxying paths that collide with client-side routes. Ambiguity here
  // would defeat that.
  headers.set('Accept', 'application/json')
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const res = await fetch(path, { ...init, headers })
  if (!res.ok) {
    let message = res.statusText
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // response body wasn't JSON (or was empty) — fall back to statusText
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

// wsURL builds a ws(s):// URL for the given path on the current origin,
// with the API key as a query param — browsers' WebSocket constructor
// can't set custom headers on the handshake request, so this is the
// same query-param pattern the backend's wsstream package expects (see
// container-api/internal/wsstream/auth.go).
export function wsURL(path: string, params: Record<string, string> = {}): string {
  const url = new URL(path, window.location.origin)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.searchParams.set('api_key', getApiKey())
  for (const [key, value] of Object.entries(params)) {
    url.searchParams.set(key, value)
  }
  return url.toString()
}
