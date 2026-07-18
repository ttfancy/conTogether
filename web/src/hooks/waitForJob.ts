import { getJob } from '../api/containers'
import type { Job } from '../api/types'

const POLL_INTERVAL_MS = 700

// Mirrors the backend's own async pattern: start/stop/delete return a
// Job ID immediately (202), and the real result only shows up once
// GET /jobs/{id} reports a terminal status. This is that poll loop,
// shared by every action button.
export async function waitForJob(jobId: string, onTick?: (job: Job) => void): Promise<Job> {
  for (;;) {
    const job = await getJob(jobId)
    onTick?.(job)
    if (job.status === 'done' || job.status === 'failed') {
      return job
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS))
  }
}
