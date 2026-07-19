export type ContainerStatus = 'pending' | 'created' | 'running' | 'stopped' | 'removed' | 'failed' | 'exited'

// "public" means readable (GET/list/log-stream) by any authenticated
// caller, not just the owner — start/stop/delete/visibility-change stay
// owner-only regardless. See container-api/internal/service's
// GetContainer vs mustOwnContainer split.
export type Visibility = 'private' | 'public'

export interface Container {
  id: string
  owner_id: string
  name: string
  image: string
  status: ContainerStatus
  visibility: Visibility
  is_owner: boolean
}

export type JobStatus = 'pending' | 'running' | 'done' | 'failed'

export interface Job {
  id: string
  status: JobStatus
  // Only ever populated for a create job — e.g. "pulling image" vs
  // "creating container" — while status is "running".
  stage?: string
  error?: string
}

export interface LogEntry {
  timestamp: string
  level: string
  message: string
  fields?: Record<string, unknown>
}

export interface Upload {
  id: string
  owner_id: string
  filename: string
  content_type: string
  size: number
  visibility: Visibility
  is_owner: boolean
}
