import { useCallback, useEffect, useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { useOutletContext } from 'react-router-dom'
import {
  ApiError,
  api,
  type AppStatus,
  type Instance,
} from '../../api/client'
import { StatusBadge } from '../../ui/StatusBadge'
import { APP_IN_FLIGHT, type AppTabContext } from './AppDetailPage'
import { fast, listItem } from '../../ui/motion'
import styles from './app.module.css'

/** The app's health at a glance: what is running, and what is not. */
export function OverviewTab() {
  const { app } = useOutletContext<AppTabContext>()
  return <HealthSection appId={app.id} appStatus={app.status} />
}

/**
 * Live state of each replica.
 *
 * Kubernetes knows which replica is crashlooping, which was OOM-killed and how
 * often each restarted; none of that reaches a user who only sees one app-level
 * badge. This is what makes the page a health panel rather than a status light.
 */
function HealthSection({
  appId,
  appStatus,
}: {
  appId: string
  appStatus: AppStatus
}) {
  const [instances, setInstances] = useState<Instance[]>([])
  const [desired, setDesired] = useState(1)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const res = await api.instances(appId)
      setInstances(res.instances)
      setDesired(res.desired)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not load instances.')
    }
  }, [appId])

  useEffect(() => {
    void refresh()
  }, [refresh, appStatus])

  // Poll while anything is unsettled — a crashloop's restart count is the
  // number a user watches to know whether it is getting worse.
  const unsettled =
    APP_IN_FLIGHT.has(appStatus) ||
    instances.some((i) => i.status !== 'running')

  useEffect(() => {
    if (!unsettled) return
    const timer = setInterval(() => void refresh(), 3000)
    return () => clearInterval(timer)
  }, [unsettled, refresh])

  const ready = instances.filter((i) => i.status === 'running').length

  return (
    <section className={styles.section}>
      <div className={styles.sectionHead}>
        <div className={styles.healthHead}>
          <h2 className={styles.sectionTitle}>Replicas</h2>
          <span className={styles.healthCount}>
            {ready}/{desired} ready
          </span>
        </div>
      </div>
      <div className={styles.panel}>
        {error && <p className={styles.emptyRow}>{error}</p>}
        {!error && instances.length === 0 && (
          <p className={styles.emptyRow}>
            Nothing running yet. Deploy to start a replica.
          </p>
        )}
        {/* Rows animate in and out because replicas genuinely appear and
            disappear during a rollout — the movement is the information. */}
        <AnimatePresence initial={false}>
        {instances.map((i) => (
          <motion.div
            key={i.name}
            layout
            variants={listItem}
            initial="hidden"
            animate="visible"
            exit={{ opacity: 0, x: -8, transition: fast }}
            className={styles.instanceRow}
          >
            <StatusBadge status={i.status} />
            <span className={styles.instanceName}>{i.name}</span>
            <span
              className={`${styles.restarts} ${
                i.restarts > 0 ? styles.restartsWarn : ''
              }`}
            >
              {i.restarts === 0
                ? 'no restarts'
                : `${i.restarts} restart${i.restarts === 1 ? '' : 's'}`}
            </span>
            <span className={styles.rowTime}>
              {i.started_at ? new Date(i.started_at).toLocaleTimeString() : '—'}
            </span>
            {/* Only shown when there is something wrong to explain. */}
            {i.status !== 'running' && (i.reason || i.message) && (
              <span className={styles.instanceReason}>
                {i.reason}
                {i.message ? ` — ${i.message}` : ''}
              </span>
            )}
          </motion.div>
        ))}
        </AnimatePresence>
      </div>
    </section>
  )
}
