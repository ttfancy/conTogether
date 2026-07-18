import { request } from './client'
import type { LogEntry } from './types'

export interface ReadLogsParams {
  level?: string
  since?: string
  until?: string
  contains?: string
}

export function readLogs(params: ReadLogsParams = {}): Promise<LogEntry[]> {
  const qs = new URLSearchParams()
  if (params.level) qs.set('level', params.level)
  if (params.since) qs.set('since', params.since)
  if (params.until) qs.set('until', params.until)
  if (params.contains) qs.set('contains', params.contains)
  const query = qs.toString()
  return request<LogEntry[]>(`/logs${query ? `?${query}` : ''}`)
}

export function clearLogs(before: string): Promise<{ status: string }> {
  return request<{ status: string }>(`/logs?before=${encodeURIComponent(before)}`, {
    method: 'DELETE',
  })
}
