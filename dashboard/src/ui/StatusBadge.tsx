import type {
  AppStatus,
  DeployStatus,
  InstanceStatus,
  ServiceStatus,
} from '../api/client'
import styles from './StatusBadge.module.css'

type Tone = 'neutral' | 'running' | 'pending' | 'failed'

// Deploy and app statuses collapse onto the same four tones, so one badge
// serves both and the dashboard reads consistently.
const TONES: Record<
  AppStatus | DeployStatus | ServiceStatus | InstanceStatus,
  Tone
> = {
  new: 'neutral',
  stopped: 'neutral',
  queued: 'pending',
  provisioning: 'pending',
  starting: 'pending',
  pending: 'pending',
  crashloop: 'failed',
  oom: 'failed',
  building: 'pending',
  deploying: 'pending',
  running: 'running',
  succeeded: 'running',
  failed: 'failed',
}

export function StatusBadge({
  status,
}: {
  status: AppStatus | DeployStatus | ServiceStatus | InstanceStatus
}) {
  const tone = TONES[status] ?? 'neutral'
  const classes = [
    styles.badge,
    styles[tone],
    tone === 'pending' && styles.pulsing,
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <span className={classes}>
      <span className={styles.dot} aria-hidden="true" />
      {status}
    </span>
  )
}
