import { request } from './client'
import type { Container, Job, Visibility } from './types'

export interface CreateContainerInput {
  image: string
  name: string
  cmd?: string[]
  env?: string[]
  visibility?: Visibility
}

export function listContainers(): Promise<Container[]> {
  return request<Container[]>('/containers')
}

export function getContainer(id: string): Promise<Container> {
  return request<Container>(`/containers/${encodeURIComponent(id)}`)
}

// CreateContainerResponse is the container (in "pending" status, no
// docker_id yet) plus the ID of the job actually doing the Docker-side
// work — poll getJob(job_id) the same way start/stop/delete already do.
export interface CreateContainerResponse extends Container {
  job_id: string
}

export function createContainer(input: CreateContainerInput): Promise<CreateContainerResponse> {
  return request<CreateContainerResponse>('/containers', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

// Create/start/stop/delete are all asynchronous on the backend — each
// submits a job and returns 202 + a Job ID immediately; the caller
// polls getJob for completion (see waitForJob).
export function startContainer(id: string): Promise<Job> {
  return request<Job>(`/containers/${encodeURIComponent(id)}/start`, { method: 'POST' })
}

export function stopContainer(id: string): Promise<Job> {
  return request<Job>(`/containers/${encodeURIComponent(id)}/stop`, { method: 'POST' })
}

export function deleteContainer(id: string): Promise<Job> {
  return request<Job>(`/containers/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function getJob(id: string): Promise<Job> {
  return request<Job>(`/jobs/${encodeURIComponent(id)}`)
}

// Owner-only on the backend regardless of the container's current
// visibility — see service.ContainerService.SetVisibility.
export function setContainerVisibility(id: string, visibility: Visibility): Promise<Container> {
  return request<Container>(`/containers/${encodeURIComponent(id)}/visibility`, {
    method: 'PUT',
    body: JSON.stringify({ visibility }),
  })
}
