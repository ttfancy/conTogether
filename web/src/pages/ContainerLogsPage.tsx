import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { wsURL } from '../api/client'

type ConnectionState = 'connecting' | 'open' | 'closed'

export default function ContainerLogsPage() {
  const { id } = useParams<{ id: string }>()
  const [lines, setLines] = useState<string[]>([])
  const [state, setState] = useState<ConnectionState>('connecting')
  const [closeReason, setCloseReason] = useState<string | null>(null)
  const logRef = useRef<HTMLPreElement>(null)

  useEffect(() => {
    if (!id) return
    setLines([])
    setState('connecting')
    setCloseReason(null)

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
    socket.onerror = () => setState('closed')

    return () => socket.close()
  }, [id])

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
