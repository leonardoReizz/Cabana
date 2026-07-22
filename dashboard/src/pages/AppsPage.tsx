import { useCallback, useEffect, useState } from 'react'
import { motion } from 'motion/react'
import { Link } from 'react-router-dom'
import { ApiError, api, type App } from '../api/client'
import { Button } from '../ui/Button'
import { StatusBadge } from '../ui/StatusBadge'
import { listContainer, listItem } from '../ui/motion'
import styles from './AppsPage.module.css'

export function AppsPage() {
  const [apps, setApps] = useState<App[] | null>(null)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      setApps(await api.listApps())
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not load apps.')
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  if (apps === null && !error) return null

  return (
    <>
      <div className={styles.head}>
        <div>
          <h1 className={styles.title}>Apps</h1>
          <p className={styles.subtitle}>
            {apps?.length
              ? `${apps.length} app${apps.length === 1 ? '' : 's'} on this cabana`
              : 'Nothing deployed yet'}
          </p>
        </div>
        <Link to="/apps/new">
          <Button variant="primary">New app</Button>
        </Link>
      </div>

      {error && <p className={styles.formError}>{error}</p>}

      {apps?.length ? (
        <motion.div
          className={styles.grid}
          variants={listContainer}
          initial="hidden"
          animate="visible"
        >
          {apps.map((app) => (
            <motion.div key={app.id} variants={listItem}>
            <Link to={`/apps/${app.id}`} className={styles.card}>
              <div className={styles.cardHead}>
                <span className={styles.name}>{app.name}</span>
                <StatusBadge status={app.status} />
              </div>
              <div className={styles.meta}>
                <span>{app.hostname || 'no hostname yet'}</span>
                <span>{repoLabel(app.repo_url)}</span>
              </div>
            </Link>
            </motion.div>
          ))}
        </motion.div>
      ) : (
        !error && (
          <div className={styles.empty}>
            <h2 className={styles.emptyTitle}>No apps yet</h2>
            <p className={styles.emptyText}>
              Point cabana at a public Git repository with a Dockerfile and it
              will build and run it for you. See the{' '}
              <Link to="/docs">docs</Link> for what your app needs.
            </p>
            <Link to="/apps/new">
              <Button variant="primary">Create your first app</Button>
            </Link>
          </div>
        )
      )}

    </>
  )
}

/** Strips the scheme so cards show `github.com/you/repo`, which is the part
 *  anyone actually reads. */
function repoLabel(url: string): string {
  return url.replace(/^https?:\/\//, '').replace(/\.git$/, '')
}
