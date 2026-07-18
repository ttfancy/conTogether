import { useEffect, useRef, useState } from 'react'
import { clearLogs, readLogs } from '../api/logs'
import { ApiError, missingApiKeyError, wsURL } from '../api/client'
import { useToast } from '../hooks/useToast'
import type { LogEntry } from '../api/types'

const LEVELS = ['DEBUG', 'INFO', 'WARN', 'ERROR']

export default function AppLogsPage() {
  const { showToast } = useToast()
  const [level, setLevel] = useState('INFO')
  const [contains, setContains] = useState('')
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [live, setLive] = useState(false)
  const socketRef = useRef<WebSocket | null>(null)

  async function search() {
    setLoading(true)
    setError(null)
    try {
      const result = await readLogs({ level, contains: contains || undefined })
      setEntries(result.reverse()) // newest first
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load logs')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    search()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Historical query (GET /logs, above) and the live tail below are two
  // different transports for the same underlying logsys.Manager — REST
  // for "what happened", WebSocket for "what's happening now". Toggling
  // live tail doesn't re-run the historical search; new entries just get
  // prepended to whatever's already on screen.
  useEffect(() => {
    if (!live) {
      socketRef.current?.close()
      socketRef.current = null
      return
    }
    const missing = missingApiKeyError()
    if (missing) {
      showToast(missing.message, 'error')
      setLive(false)
      return
    }
    const socket = new WebSocket(wsURL('/ws/logs'))
    socketRef.current = socket
    socket.onmessage = (event) => {
      const entry = JSON.parse(event.data as string) as LogEntry
      setEntries((prev) => [entry, ...prev])
    }
    // onerror fires for a genuine connection/auth failure (the server
    // rejects the handshake outright on an invalid API key); a plain
    // client-initiated close via the cleanup below only triggers
    // onclose, never onerror — so this can't misfire just from toggling
    // the checkbox off.
    socket.onerror = () => showToast('Live tail connection failed — check your API key.', 'error')
    socket.onclose = () => setLive(false)
    return () => socket.close()
  }, [live, showToast])

  async function handleClear() {
    if (!window.confirm('Clear all log entries older than now? This cannot be undone.')) return
    try {
      await clearLogs(new Date().toISOString())
      showToast('Logs cleared', 'success')
      await search()
    } catch (err) {
      showToast(err instanceof ApiError ? err.message : 'Failed to clear logs', 'error')
    }
  }

  return (
    <div>
      <h2>Application logs</h2>
      <p className="muted">container-api's own operational logs — request lifecycle, container events, job outcomes.</p>

      <form
        className="card create-form"
        onSubmit={(e) => {
          e.preventDefault()
          search()
        }}
      >
        <select value={level} onChange={(e) => setLevel(e.target.value)}>
          {LEVELS.map((l) => (
            <option key={l} value={l}>
              {l}+
            </option>
          ))}
        </select>
        <input
          value={contains}
          onChange={(e) => setContains(e.target.value)}
          placeholder="message contains…"
        />
        <button type="submit" disabled={loading}>
          {loading ? 'Searching…' : 'Search'}
        </button>
        <label className="live-toggle">
          <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} />
          Live tail
        </label>
        <button type="button" className="danger" onClick={handleClear}>
          Clear logs
        </button>
      </form>

      {error && <p className="error-banner">{error}</p>}

      <table className="data-table">
        <thead>
          <tr>
            <th>Time</th>
            <th>Level</th>
            <th>Message</th>
            <th>Fields</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((e, i) => (
            <tr key={i}>
              <td className="mono">{new Date(e.timestamp).toLocaleTimeString()}</td>
              <td>
                <span className={`level-badge level-${e.level.toLowerCase()}`}>{e.level}</span>
              </td>
              <td>{e.message}</td>
              <td className="mono muted">
                {e.fields ? JSON.stringify(e.fields) : ''}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {entries.length === 0 && !loading && <p className="empty-state">No entries match.</p>}
    </div>
  )
}
