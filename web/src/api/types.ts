export type ContainerStatus = 'created' | 'running' | 'stopped' | 'removed'

export interface Container {
  id: string
  name: string
  image: string
  status: ContainerStatus
}

export type JobStatus = 'pending' | 'running' | 'done' | 'failed'

export interface Job {
  id: string
  status: JobStatus
  error?: string
}

export interface LogEntry {
  timestamp: string
  level: string
  message: string
  fields?: Record<string, unknown>
}
