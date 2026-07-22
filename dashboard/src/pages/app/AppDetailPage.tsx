import { useCallback, useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useNavigate, useParams } from 'react-router-dom'
import {
  ApiError,
  api,
  type App,
  type AppStatus,
  type Deploy,
} from '../../api/client'
import { Button } from '../../ui/Button'
import { StatusBadge } from '../../ui/StatusBadge'
import { useConfirm } from '../../ui/AlertDialog'
import styles from './app.module.css'

/** Shared with every tab through the router's outlet context, so the app is
 *  fetched once for the page rather than once per tab. */
export interface AppTabContext {
  app: App
  deploys: Deploy[]
  refresh: () => Promise<void>
}

/** App statuses that mean work is happening and the page should keep polling. */
export const APP_IN_FLIGHT = new Set<AppStatus>(['building', 'deploying'])

const TABS = [
  { to: '', label: 'Dashboard', end: true },
  { to: 'logs', label: 'Logs' },
  { to: 'settings', label: 'Settings' },
]

/**
 * Shell for one app: identity and the actions that apply to the whole app,
 * with the detail split across tabs.
 *
 * Tabs are routes rather than local state, so a reload keeps you where you
 * were and a link to a specific tab is shareable — which matters most for the
 * one you send someone when something is broken.
 */
export function AppDetailPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const confirm = useConfirm()

  const [app, setApp] = useState<App | null>(null)
  const [deploys, setDeploys] = useState<Deploy[]>([])
  const [error, setError] = useState('')
  const [deploying, setDeploying] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const [a, d] = await Promise.all([api.getApp(id), api.listDeploys(id)])
      setApp(a)
      setDeploys(d)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not load app.')
    }
  }, [id])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // Poll only while something is actually in flight. A steady-state dashboard
  // should not generate traffic.
  useEffect(() => {
    if (!app || !APP_IN_FLIGHT.has(app.status)) return
    const timer = setInterval(() => void refresh(), 1500)
    return () => clearInterval(timer)
  }, [app, refresh])

  async function handleDeploy() {
    setDeploying(true)
    setError('')
    try {
      await api.deploy(id)
      await refresh()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not deploy.')
    } finally {
      setDeploying(false)
    }
  }

  async function handleDelete() {
    if (!app) return
    const confirmed = await confirm({
      title: `Delete ${app.name}?`,
      description:
        'The app, its containers and its build history are removed from this cabana.',
      consequence:
        'Its Git repository is untouched, but every deployed image and stored build log is destroyed.',
      confirmLabel: 'Delete app',
    })
    if (!confirmed) return
    try {
      await api.deleteApp(id)
      void navigate('/apps')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not delete app.')
    }
  }

  if (!app) return error ? <p role="alert">{error}</p> : null

  return (
    <>
      <Link to="/apps" className={styles.back}>
        ← Apps
      </Link>

      <div className={styles.head}>
        <div>
          <div className={styles.titleRow}>
            <h1 className={styles.title}>{app.name}</h1>
            <StatusBadge status={app.status} />
          </div>
          <p className={styles.hostname}>
            {app.hostname ? (
              <a href={app.url ?? `http://${app.hostname}`} target="_blank" rel="noreferrer">
                {app.hostname}
              </a>
            ) : (
              'no hostname yet — deploy to get one'
            )}{' '}
            · {app.git_ref}
          </p>
        </div>
        <div className={styles.actions}>
          <Button
            variant="primary"
            onClick={() => void handleDeploy()}
            loading={deploying}
          >
            Deploy
          </Button>
          <Button variant="danger" onClick={() => void handleDelete()}>
            Delete
          </Button>
        </div>
      </div>

      <nav className={styles.tabNav} aria-label="App sections">
        {TABS.map((tab) => (
          <NavLink
            key={tab.label}
            to={tab.to}
            end={tab.end}
            className={({ isActive }) =>
              isActive ? `${styles.tabLink} ${styles.tabLinkActive}` : styles.tabLink
            }
          >
            {tab.label}
          </NavLink>
        ))}
      </nav>

      {error && <p role="alert">{error}</p>}

      <Outlet context={{ app, deploys, refresh } satisfies AppTabContext} />
    </>
  )
}
