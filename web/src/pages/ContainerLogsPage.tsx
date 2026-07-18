import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { missingApiKeyError, wsURL } from '../api/client'
import { useToast } from '../hooks/useToast'

type ConnectionState = 'connecting' | 'open' | 'closed'

export default function ContainerLogsPage() {
  const { id } = useParams<{ id: string }>()
  const { showToast } = useToast()
  const [lines, setLines] = useState<string[]>([])
  const [state, setState] = useState<ConnectionState>('connecting')
  const [closeReason, setCloseReason] = useState<string | null>(null)
  const logRef = useRef<HTMLPreElement>(null)

  useEffect(() => {
    if (!id) return
    setLines([])
    setState('connecting')
    setCloseReason(null)

    const missing = missingApiKeyError()
    if (missing) {
      setState('closed')
      setCloseReason(missing.message)
      showToast(missing.message, 'error')
      return
    }

    // Live-tails via WebSocket rather than the SSE endpoint the REST API
    // also exposes: a browser's native EventSource can't set custom
    // headers OR pass auth any other way than a query param handled
    // specially — the WebSocket path already supports that (see
    // container-api/internal/wsstream), so it's the simpler choice for
    // a plain browser client, not a functional difference in the log
    // content itself (same backend stream either way).
    const socket = new WebSocket(wsURL(`/ws/containers/${id}/logs`, { tail: '200' }))

    socket.onopen = () => setState('open')
    socket.onmessage = (event) => {
      setLines((prev) => [...prev, event.data as string])
    }
    socket.onclose = (event) => {
      setState('closed')
      if (event.reason) setCloseReason(event.reason)
    }
    // A genuine connection/auth failure — not the cleanup-triggered
    // close below, which only fires onclose — so a toast here always
    // means something actually went wrong, not just navigating away.
    socket.onerror = () => {
      setState('closed')
      showToast('Log stream connection failed.', 'error')
    }

    return () => socket.close()
  }, [id, showToast])

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
  }, [lines])

  return (
    <div>
      <p>
        <Link to="/">&larr; Containers</Link>
      </p>
      <h2>Container logs</h2>
      <p className="muted">
        {id} — <ConnectionIndicator state={state} />
        {closeReason && <span className="error-inline"> ({closeReason})</span>}
      </p>
      <pre className="log-viewer" ref={logRef}>
        {lines.length === 0 ? '(waiting for output…)' : lines.join('\n')}
      </pre>
    </div>
  )
}

function ConnectionIndicator({ state }: { state: ConnectionState }) {
  const label = state === 'open' ? 'live' : state === 'connecting' ? 'connecting…' : 'disconnected'
  return <span className={`conn-indicator conn-${state}`}>{label}</span>
}
