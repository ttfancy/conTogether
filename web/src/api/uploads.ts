import { ApiError, getApiKey, request } from './client'
import type { Upload, Visibility } from './types'

// Not routed through request() — that helper always sets
// Content-Type: application/json, which would corrupt a multipart body.
export async function uploadFile(file: File, visibility: Visibility = 'private'): Promise<Upload> {
  const form = new FormData()
  form.append('file', file)
  form.append('visibility', visibility)

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
  return (await res.json()) as Upload
}

// Everything the caller may read: their own uploads (any visibility)
// plus everyone else's public ones — same "owner, or public" model as
// containers.
export function listUploads(): Promise<Upload[]> {
  return request<Upload[]>('/uploads')
}

export function setUploadVisibility(id: string, visibility: Visibility): Promise<Upload> {
  return request<Upload>(`/uploads/${encodeURIComponent(id)}/visibility`, {
    method: 'PUT',
    body: JSON.stringify({ visibility }),
  })
}

// Not routed through request()/a plain <a href>: the download needs the
// X-API-Key header, and browsers can't attach custom headers to a
// normal navigation — so this fetches the file as a blob and triggers
// the save via a throwaway object URL instead.
export async function downloadUpload(id: string, filename: string): Promise<void> {
  const headers = new Headers()
  headers.set('X-API-Key', getApiKey())

  const res = await fetch(`/uploads/${encodeURIComponent(id)}`, { headers })
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

  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
