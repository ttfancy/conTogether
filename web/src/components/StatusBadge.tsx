const COLORS: Record<string, string> = {
  created: '#8a8f98',
  running: '#2e9e5b',
  stopped: '#c77d16',
  removed: '#b3261e',
  pending: '#8a8f98',
  done: '#2e9e5b',
  failed: '#b3261e',
}

export default function StatusBadge({ status }: { status: string }) {
  const color = COLORS[status] ?? '#8a8f98'
  return (
    <span className="status-badge" style={{ '--badge-color': color } as React.CSSProperties}>
      {status}
    </span>
  )
}
