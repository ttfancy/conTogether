import { useRef, useState } from 'react'
import { uploadFile } from '../api/uploads'
import { ApiError } from '../api/client'

interface UploadRecord {
  filename: string
  path?: string
  error?: string
}

export default function UploadsPage() {
  const [uploading, setUploading] = useState(false)
  const [history, setHistory] = useState<UploadRecord[]>([])
  const inputRef = useRef<HTMLInputElement>(null)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const file = inputRef.current?.files?.[0]
    if (!file) return

    setUploading(true)
    try {
      const result = await uploadFile(file)
      setHistory((prev) => [{ filename: file.name, path: result.path }, ...prev])
      if (inputRef.current) inputRef.current.value = ''
    } catch (err) {
      const message = err instanceof ApiError ? err.message : 'Upload failed'
      setHistory((prev) => [{ filename: file.name, error: message }, ...prev])
    } finally {
      setUploading(false)
    }
  }

  return (
    <div>
      <h2>Uploads</h2>
      <p className="muted">
        Accepts CSV/JSON/source code and images (PNG/JPEG/GIF) — content is sniffed server-side,
        not trusted from the file extension. Stored under a per-user directory.
      </p>

      <form className="card create-form" onSubmit={handleSubmit}>
        <input ref={inputRef} type="file" required />
        <button type="submit" disabled={uploading}>
          {uploading ? 'Uploading…' : 'Upload'}
        </button>
      </form>

      {history.length > 0 && (
        <table className="data-table">
          <thead>
            <tr>
              <th>File</th>
              <th>Result</th>
            </tr>
          </thead>
          <tbody>
            {history.map((h, i) => (
              <tr key={i}>
                <td>{h.filename}</td>
                <td>
                  {h.path ? (
                    <code>{h.path}</code>
                  ) : (
                    <span className="error-inline">{h.error}</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
