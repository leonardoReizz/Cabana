import { useState } from 'react'
import type { ReactNode } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useAuth } from '../auth'
import { Button } from '../ui/Button'
import { Logo } from '../ui/Logo'
import { applyTheme, resolveTheme } from '../theme'
import styles from './Shell.module.css'

export function Shell({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth()
  const [theme, setTheme] = useState(resolveTheme)

  function toggleTheme() {
    const next = theme === 'dark' ? 'light' : 'dark'
    applyTheme(next)
    setTheme(next)
  }

  return (
    <div className={styles.shell}>
      <header className={styles.header}>
        <div className={styles.headerInner}>
          <Link to="/apps" className={styles.brand}>
            <Logo />
            cabana
          </Link>
          <span className={styles.version}>v0.1</span>

          <nav className={styles.links}>
            <NavLink
              to="/apps"
              className={({ isActive }) =>
                isActive ? `${styles.link} ${styles.linkActive}` : styles.link
              }
            >
              Apps
            </NavLink>
            <NavLink
              to="/services"
              className={({ isActive }) =>
                isActive ? `${styles.link} ${styles.linkActive}` : styles.link
              }
            >
              Services
            </NavLink>
            <NavLink
              to="/settings"
              className={({ isActive }) =>
                isActive ? `${styles.link} ${styles.linkActive}` : styles.link
              }
            >
              Settings
            </NavLink>
            <NavLink
              to="/docs"
              className={({ isActive }) =>
                isActive ? `${styles.link} ${styles.linkActive}` : styles.link
              }
            >
              Docs
            </NavLink>
          </nav>

          <div className={styles.spacer} />

          <Button
            variant="ghost"
            size="sm"
            onClick={toggleTheme}
            aria-label={`Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`}
          >
            {theme === 'dark' ? '☀' : '☾'}
          </Button>
          <span className={styles.user}>{user?.username}</span>
          <Button variant="ghost" size="sm" onClick={() => void logout()}>
            Sign out
          </Button>
        </div>
      </header>

      <main className={styles.main}>{children}</main>
    </div>
  )
}
