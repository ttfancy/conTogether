import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { missingApiKeyError, wsURL } from '../api/client'
import { useToast } from '../hooks/useToast'

type ConnectionState = 'connecting' | 'open' | 'closed'

// Wire protocol (see container-api/internal/wsstream/containerexec.go):
// binary WS frames carry raw PTY bytes both directions, text WS frames
// from the client are JSON resize control messages. A single WebSocket
// needs some way to tell "this is terminal input" from "this is a
// control message" apart — this is the split it uses.
function sendResize(socket: WebSocket, term: Terminal) {
  socket.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
}

export default function ContainerExecPage() {
  const { id } = useParams<{ id: string }>()
  const { showToast } = useToast()
  const [state, setState] = useState<ConnectionState>('connecting')
  const [closeReason, setCloseReason] = useState<string | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!id || !containerRef.current) return
    setState('connecting')
    setCloseReason(null)

    const missing = missingApiKeyError()
    if (missing) {
      setState('closed')
      setCloseReason(missing.message)
      showToast(missing.message, 'error')
      return
    }

    const term = new Terminal({
      convertEol: true,
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, SF Mono, Consolas, monospace',
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(containerRef.current)
    fitAddon.fit()

    const socket = new WebSocket(wsURL(`/ws/containers/${id}/exec`))
    socket.binaryType = 'arraybuffer'

    socket.onopen = () => {
      setState('open')
      sendResize(socket, term)
      term.focus()
    }
    socket.onmessage = (event) => {
      term.write(new Uint8Array(event.data as ArrayBuffer))
    }
    socket.onclose = (event) => {
      setState('closed')
      if (event.reason) setCloseReason(event.reason)
      term.write('\r\n\x1b[31m[session closed]\x1b[0m\r\n')
    }
    socket.onerror = () => {
      setState('closed')
      showToast('Terminal connection failed.', 'error')
    }

    const dataDisposable = term.onData((data) => {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(new TextEncoder().encode(data))
      }
    })

    // Keep the PTY size in sync with however large the terminal actually
    // renders — without this, full-screen programs (vim, top, ...) draw
    // at whatever size the session happened to start at instead of the
    // real element size.
    const resizeObserver = new ResizeObserver(() => {
      fitAddon.fit()
      if (socket.readyState === WebSocket.OPEN) {
        sendResize(socket, term)
      }
    })
    resizeObserver.observe(containerRef.current)

    return () => {
      resizeObserver.disconnect()
      dataDisposable.dispose()
      socket.close()
      term.dispose()
    }
  }, [id, showToast])

  return (
    <div>
      <p>
        <Link to="/">&larr; Containers</Link>
      </p>
      <h2>Container terminal</h2>
      <p className="muted">
        {id} — <ConnectionIndicator state={state} />
        {closeReason && <span className="error-inline"> ({closeReason})</span>}
      </p>
      <div className="terminal-viewer" ref={containerRef} />
    </div>
  )
}

function ConnectionIndicator({ state }: { state: ConnectionState }) {
  const label = state === 'open' ? 'live' : state === 'connecting' ? 'connecting…' : 'disconnected'
  return <span className={`conn-indicator conn-${state}`}>{label}</span>
}
