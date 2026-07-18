import { useCallback, useEffect, useRef, useState } from 'react'
import {
  downloadUpload,
  listUploads,
  setUploadVisibility,
  uploadFile,
} from '../api/uploads'
import { ApiError } from '../api/client'
import type { Upload, Visibility } from '../api/types'

export default function UploadsPage() {
  const [uploads, setUploads] = useState<Upload[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [uploading, setUploading] = useState(false)
  const [visibility, setVisibility] = useState<Visibility>('private')
  const [togglingVisibility, setTogglingVisibility] = useState<Record<string, boolean>>({})
  const [downloading, setDownloading] = useState<Record<string, boolean>>({})
  const inputRef = useRef<HTMLInputElement>(null)

  const refresh = useCallback(async () => {
    try {
      const list = await listUploads()
      setUploads(list)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load uploads')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const file = inputRef.current?.files?.[0]
    if (!file) return

    setUploading(true)
    try {
      await uploadFile(file, visibility)
      if (inputRef.current) inputRef.current.value = ''
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Upload failed')
    } finally {
      setUploading(false)
    }
  }

  async function toggleVisibility(u: Upload) {
    const next: Visibility = u.visibility === 'public' ? 'private' : 'public'
    setTogglingVisibility((p) => ({ ...p, [u.id]: true }))
    try {
      await setUploadVisibility(u.id, next)
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to change visibility')
    } finally {
      setTogglingVisibility((p) => {
        const copy = { ...p }
        delete copy[u.id]
        return copy
      })
    }
  }

  async function handleDownload(u: Upload) {
    setDownloading((p) => ({ ...p, [u.id]: true }))
    try {
      await downloadUpload(u.id, u.filename)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Download failed')
    } finally {
      setDownloading((p) => {
        const copy = { ...p }
        delete copy[u.id]
        return copy
      })
    }
  }

  return (
    <div>
      <h2>Uploads</h2>
      <p className="muted">
        Accepts CSV/JSON/source code and images (PNG/JPEG/GIF) — content is sniffed server-side,
        not trusted from the file extension. Stored under a per-user directory. Public uploads can
        be downloaded by anyone; only you can change an upload's visibility.
      </p>

      <form className="card create-form" onSubmit={handleSubmit}>
        <input ref={inputRef} type="file" required />
        <select value={visibility} onChange={(e) => setVisibility(e.target.value as Visibility)}>
          <option value="private">private</option>
          <option value="public">public</option>
        </select>
        <button type="submit" disabled={uploading}>
          {uploading ? 'Uploading…' : 'Upload'}
        </button>
      </form>

      {error && <p className="error-banner">{error}</p>}

      {loading ? (
        <p>Loading…</p>
      ) : uploads.length === 0 ? (
        <p className="empty-state">No uploads yet — upload one above.</p>
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>File</th>
              <th>Owner</th>
              <th>Type</th>
              <th>Size</th>
              <th>Visibility</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {uploads.map((u) => (
              <tr key={u.id}>
                <td>{u.filename}</td>
                <td>{u.is_owner ? 'you' : u.owner_id}</td>
                <td className="mono">{u.content_type}</td>
                <td>{u.size.toLocaleString()} B</td>
                <td>
                  {u.is_owner ? (
                    <button
                      className="visibility-toggle"
                      disabled={!!togglingVisibility[u.id]}
                      onClick={() => toggleVisibility(u)}
                      title="Click to toggle — public uploads are downloadable by anyone, but only you can change that"
                    >
                      {u.visibility}
                    </button>
                  ) : (
                    <span className="muted">{u.visibility}</span>
                  )}
                </td>
                <td className="row-actions">
                  <button disabled={!!downloading[u.id]} onClick={() => handleDownload(u)}>
                    {downloading[u.id] ? 'Downloading…' : 'Download'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
