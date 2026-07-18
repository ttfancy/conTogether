import { request } from './client'
import type { Container, Job } from './types'

export interface CreateContainerInput {
  image: string
  name: string
  cmd?: string[]
  env?: string[]
}

export function listContainers(): Promise<Container[]> {
  return request<Container[]>('/containers')
}

export function getContainer(id: string): Promise<Container> {
  return request<Container>(`/containers/${encodeURIComponent(id)}`)
}

export function createContainer(input: CreateContainerInput): Promise<Container> {
  return request<Container>('/containers', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

// Start/stop/delete are all asynchronous on the backend — each submits
// a job and returns 202 + a Job ID immediately; the caller polls getJob
// for completion (see useJobPolling).
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
