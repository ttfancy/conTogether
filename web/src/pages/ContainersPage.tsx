import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  createContainer,
  deleteContainer,
  listContainers,
  setContainerVisibility,
  startContainer,
  stopContainer,
} from '../api/containers'
import { ApiError } from '../api/client'
import { waitForJob } from '../hooks/waitForJob'
import { useApiKey } from '../hooks/useApiKey'
import type { Container, Visibility } from '../api/types'
import StatusBadge from '../components/StatusBadge'

export default function ContainersPage() {
  const { apiKey } = useApiKey()
  const [containers, setContainers] = useState<Container[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  // Per-container "a job is in flight" status, shown as a transient
  // label until the poll resolves and the list is refreshed.
  const [pending, setPending] = useState<Record<string, string>>({})

  const [image, setImage] = useState('alpine')
  const [name, setName] = useState('')
  const [command, setCommand] = useState('')
  const [visibility, setVisibility] = useState<Visibility>('private')
  const [creating, setCreating] = useState(false)
  // Per-container "visibility toggle in flight" — separate from
  // `pending` (job-based start/stop/delete), since this is a plain
  // synchronous request, not a job to poll.
  const [togglingVisibility, setTogglingVisibility] = useState<Record<string, boolean>>({})

  const refresh = useCallback(async () => {
    try {
      const list = await listContainers()
      setContainers(list)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load containers')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  async function runAction(id: string, label: string, action: () => Promise<{ id: string }>) {
    setPending((p) => ({ ...p, [id]: label }))
    try {
      const job = await action()
      await waitForJob(job.id, (j) => setPending((p) => ({ ...p, [id]: `${label} (${j.status})` })))
    } catch (err) {
      setError(err instanceof ApiError ? err.message : `Failed to ${label} container`)
    } finally {
      setPending((p) => {
        const next = { ...p }
        delete next[id]
        return next
      })
      refresh()
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    if (!apiKey.trim()) {
      window.alert('Enter an API key (top right) before creating a container.')
      return
    }
    if (!name.trim() || !image.trim()) return
    setCreating(true)
    try {
      // Wrapped in `sh -c` so the field takes a plain shell command line
      // (pipes, loops, quoting — whatever you'd type in a terminal)
      // rather than requiring the backend's exec-form []string directly.
      const cmd = command.trim() ? ['sh', '-c', command.trim()] : undefined
      await createContainer({ image: image.trim(), name: name.trim(), cmd, visibility })
      setName('')
      setCommand('')
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to create container')
    } finally {
      setCreating(false)
    }
  }

  async function toggleVisibility(c: Container) {
    const next: Visibility = c.visibility === 'public' ? 'private' : 'public'
    setTogglingVisibility((p) => ({ ...p, [c.id]: true }))
    try {
      await setContainerVisibility(c.id, next)
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to change visibility')
    } finally {
      setTogglingVisibility((p) => {
        const copy = { ...p }
        delete copy[c.id]
        return copy
      })
    }
  }

  return (
    <div>
      <h2>Containers</h2>

      <form className="card create-form" onSubmit={handleCreate}>
        <input
          value={image}
          onChange={(e) => setImage(e.target.value)}
          placeholder="image (e.g. alpine)"
        />
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="name" />
        <input
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          placeholder='command (optional, e.g. while true; do echo hi; sleep 1; done)'
          className="command-input"
        />
        <select value={visibility} onChange={(e) => setVisibility(e.target.value as Visibility)}>
          <option value="private">private</option>
          <option value="public">public</option>
        </select>
        <button type="submit" disabled={creating}>
          {creating ? 'Creating…' : 'Create container'}
        </button>
      </form>

      {error && <p className="error-banner">{error}</p>}

      {loading ? (
        <p>Loading…</p>
      ) : containers.length === 0 ? (
        <p className="empty-state">No containers yet — create one above.</p>
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Image</th>
              <th>Owner</th>
              <th>Status</th>
              <th>Visibility</th>
              <th>Logs</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {containers.map((c) => (
              <tr key={c.id}>
                <td>{c.name}</td>
                <td>{c.image}</td>
                <td>{c.is_owner ? 'you' : c.owner_id}</td>
                <td>
                  <StatusBadge status={pending[c.id] ?? c.status} />
                </td>
                <td>
                  {c.is_owner ? (
                    <button
                      className="visibility-toggle"
                      disabled={!!togglingVisibility[c.id]}
                      onClick={() => toggleVisibility(c)}
                      title="Click to toggle — public containers are readable by anyone, but only you can start/stop/delete them"
                    >
                      {c.visibility}
                    </button>
                  ) : (
                    <span className="muted">{c.visibility}</span>
                  )}
                </td>
                <td>
                  <Link to={`/containers/${c.id}/logs`}>view</Link>
                </td>
                <td className="row-actions">
                  {c.is_owner ? (
                    <>
                      <button
                        disabled={!!pending[c.id]}
                        onClick={() => runAction(c.id, 'start', () => startContainer(c.id))}
                      >
                        Start
                      </button>
                      <button
                        disabled={!!pending[c.id]}
                        onClick={() => runAction(c.id, 'stop', () => stopContainer(c.id))}
                      >
                        Stop
                      </button>
                      <button
                        disabled={!!pending[c.id]}
                        className="danger"
                        onClick={() => runAction(c.id, 'delete', () => deleteContainer(c.id))}
                      >
                        Delete
                      </button>
                    </>
                  ) : (
                    <span className="muted">view only</span>
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
