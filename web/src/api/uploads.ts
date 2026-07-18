import { ApiError, getApiKey } from './client'

export interface UploadResult {
  path: string
}

// Not routed through request() — that helper always sets
// Content-Type: application/json, which would corrupt a multipart body.
export async function uploadFile(file: File): Promise<UploadResult> {
  const form = new FormData()
  form.append('file', file)

  const headers = new Headers()
  headers.set('X-API-Key', getApiKey())
  headers.set('Accept', 'application/json')

  const res = await fetch('/uploads', { method: 'POST', body: form, headers })
  if (!res.ok) {
    let message = res.statusText
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // ignore non-JSON error bodies
    }
    throw new ApiError(res.status, message)
  }
  return (await res.json()) as UploadResult
}
