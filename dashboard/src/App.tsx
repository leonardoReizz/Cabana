import { Navigate, Route, Routes } from 'react-router-dom'
import { useAuth } from './auth'
import { Shell } from './layout/Shell'
import { LoginPage } from './pages/LoginPage'
import { AppsPage } from './pages/AppsPage'
import { NewAppPage } from './pages/NewAppPage'
import { AppDetailPage } from './pages/app/AppDetailPage'
import { OverviewTab } from './pages/app/OverviewTab'
import { LogsTab } from './pages/app/LogsTab'
import { SettingsTab } from './pages/app/SettingsTab'
import { DocsPage } from './pages/DocsPage'
import { ServicesPage } from './pages/ServicesPage'
import { ServiceDetailPage } from './pages/ServiceDetailPage'
import { SettingsPage } from './pages/SettingsPage'

export function App() {
  const { user, loading } = useAuth()

  // Render nothing while the stored session is being verified. It is a
  // sub-second gap, and a spinner here reads as jank rather than progress.
  if (loading) return null

  if (!user) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    )
  }

  return (
    <Shell>
      <Routes>
        <Route path="/apps" element={<AppsPage />} />
        <Route path="/apps/new" element={<NewAppPage />} />
        <Route path="/apps/:id" element={<AppDetailPage />}>
          <Route index element={<OverviewTab />} />
          <Route path="logs" element={<LogsTab />} />
          <Route path="settings" element={<SettingsTab />} />
        </Route>
        <Route path="/services" element={<ServicesPage />} />
        <Route path="/services/:id" element={<ServiceDetailPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/docs" element={<DocsPage />} />
        <Route path="*" element={<Navigate to="/apps" replace />} />
      </Routes>
    </Shell>
  )
}
