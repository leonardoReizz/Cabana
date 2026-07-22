/**
 * Typed client for the cabana API. Every network call in the dashboard goes
 * through here — the contract lives in one file so the v0.5 CLI and this stay
 * describable by the same document.
 */

export type AppStatus =
  | 'new'
  | 'building'
  | 'deploying'
  | 'running'
  | 'failed'
  | 'stopped'

export type HealthType = 'tcp' | 'http'

export interface Health {
  type: HealthType
  path: string
  initial_delay_seconds: number
  /** Gap between checks. Sets how quickly a failure is noticed. */
  period_seconds: number
}

export interface App {
  id: string
  name: string
  repo_url: string
  git_ref: string
  status: AppStatus
  hostname: string
  /** Hostname with the scheme actually in force — https only when TLS is on. */
  url?: string
  replicas: number
  health: Health
  created_at: string
  updated_at: string
}

export type InstanceStatus =
  | 'running'
  | 'starting'
  | 'pending'
  | 'crashloop'
  | 'oom'
  | 'failed'

export interface Instance {
  name: string
  status: InstanceStatus
  ready: boolean
  restarts: number
  reason?: string
  message?: string
  started_at?: string
}

export type DeployStatus =
  | 'queued'
  | 'building'
  | 'deploying'
  | 'succeeded'
  | 'failed'

export interface Deploy {
  id: string
  app_id: string
  status: DeployStatus
  git_ref: string
  image?: string
  rolled_back_from?: string
  commit_sha?: string
  message?: string
  created_at: string
  finished_at?: string
}

export interface User {
  id: string
  username: string
  created_at: string
}

export interface LogLine {
  ts: string
  source: 'build' | 'app' | 'cabana'
  text: string
}

export interface CatalogEntry {
  name: string
  display_name: string
  description: string
  category: string
  image: string
  port: number
  storage: string
  env_var: string
  /** The template's pinned tag, offered as the version input's placeholder. */
  default_version: string
}

export type ServiceStatus = 'provisioning' | 'running' | 'failed'

export interface Service {
  id: string
  name: string
  template: string
  status: ServiceStatus
  /** The exact image running, e.g. "redis:7.2-alpine". */
  image: string
  backup_schedule: 'off' | 'daily' | 'weekly'
  backup_hour: number
  host: string
  port: number
  message?: string
  created_at: string
  updated_at: string
}

export interface ServiceCredentials {
  env_var: string
  connection_string: string
  host: string
  port: number
  secrets: Record<string, string>
}

export interface BackupConfig {
  endpoint: string
  region: string
  bucket: string
  access_key: string
  retention_count: number
  configured: boolean
}

export interface StateBackup {
  id: string
  status: BackupStatus
  object_key: string
  size_bytes: number
  error?: string
  created_at: string
  finished_at?: string
}

export type BackupStatus = 'running' | 'succeeded' | 'failed'

export interface Backup {
  id: string
  service_id: string
  service_name: string
  template: string
  status: BackupStatus
  object_key: string
  size_bytes: number
  error?: string
  created_at: string
  finished_at?: string
}

export interface ServiceLink {
  service_id: string
  service_name: string
  template: string
  env_var: string
  created_at: string
}

/** ApiError carries the server's error code so callers can branch on it
 *  (`name_taken`, `unauthorized`) instead of matching on message strings. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

const TOKEN_KEY = 'cabana.token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string | null): void {
  if (token === null) localStorage.removeItem(TOKEN_KEY)
  else localStorage.setItem(TOKEN_KEY, token)
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (res.status === 204) return undefined as T

  const text = await res.text()
  const payload: unknown = text ? JSON.parse(text) : null

  if (!res.ok) {
    const err = payload as { error?: string; message?: string } | null
    throw new ApiError(
      res.status,
      err?.error ?? 'unknown',
      err?.message ?? `request failed with ${res.status}`,
    )
  }
  return payload as T
}

export const api = {
  login: (username: string, password: string) =>
    request<{ token: string; expires_at: string; user: User }>(
      'POST',
      '/auth/login',
      { username, password },
    ),

  logout: () => request<void>('POST', '/auth/logout'),

  me: () => request<User>('GET', '/auth/me'),

  listApps: () =>
    request<{ apps: App[] }>('GET', '/apps').then((r) => r.apps),

  /** Creates a fully configured app in one request. Everything past the first
   *  three fields is optional. */
  createApp: (input: {
    name: string
    repo_url: string
    git_ref?: string
    build_args?: Record<string, string>
    env?: Record<string, string>
    service_ids?: string[]
    replicas?: number
    health?: Health
  }) => request<App>('POST', '/apps', input),

  getBuildArgs: (id: string) =>
    request<{ build_args: Record<string, string> }>(
      'GET',
      `/apps/${id}/build-args`,
    ).then((r) => r.build_args),

  putBuildArgs: (id: string, args: Record<string, string>) =>
    request<{ build_args: Record<string, string> }>(
      'PUT',
      `/apps/${id}/build-args`,
      { build_args: args },
    ),

  getApp: (id: string) => request<App>('GET', `/apps/${id}`),

  deleteApp: (id: string) => request<void>('DELETE', `/apps/${id}`),

  getEnv: (id: string) =>
    request<{ env: Record<string, string> }>('GET', `/apps/${id}/env`).then(
      (r) => r.env,
    ),

  putEnv: (id: string, env: Record<string, string>) =>
    request<{ env: Record<string, string> }>('PUT', `/apps/${id}/env`, { env }),

  listDeploys: (id: string) =>
    request<{ deploys: Deploy[] }>('GET', `/apps/${id}/deploys`).then(
      (r) => r.deploys,
    ),

  deploy: (id: string) => request<Deploy>('POST', `/apps/${id}/deploys`),

  /** Restores a previous deploy's image. No rebuild, so it is near-instant. */
  rollback: (id: string, deployId: string) =>
    request<Deploy>('POST', `/apps/${id}/deploys`, { rollback_to: deployId }),

  updateSettings: (id: string, settings: { replicas: number; health: Health }) =>
    request<App>('PUT', `/apps/${id}/settings`, settings),

  instances: (id: string) =>
    request<{ instances: Instance[]; desired: number }>(
      'GET',
      `/apps/${id}/instances`,
    ),

  /** Stored build output for one deploy. Available after the deploy finished,
   *  unlike the live stream, which only exists while a browser is watching. */
  deployLog: (appId: string, deployId: string) =>
    request<{ log: string; truncated: boolean; status: DeployStatus }>(
      'GET',
      `/apps/${appId}/deploys/${deployId}/log`,
    ),

  // ---- services ----

  catalog: () =>
    request<{ templates: CatalogEntry[] }>('GET', '/catalog').then(
      (r) => r.templates,
    ),

  listServices: () =>
    request<{ services: Service[] }>('GET', '/services').then(
      (r) => r.services,
    ),

  createService: (input: {
    name: string
    template: string
    /** Image tag. Empty means the template's pinned default. */
    version?: string
  }) =>
    request<Service>('POST', '/services', input),

  getService: (id: string) => request<Service>('GET', `/services/${id}`),

  deleteService: (id: string) => request<void>('DELETE', `/services/${id}`),

  serviceCredentials: (id: string) =>
    request<ServiceCredentials>('GET', `/services/${id}/credentials`),

  // ---- links ----

  listLinks: (appId: string) =>
    request<{ links: ServiceLink[] }>('GET', `/apps/${appId}/links`).then(
      (r) => r.links,
    ),

  linkService: (appId: string, serviceId: string) =>
    request<{ env_var: string; service_name: string }>(
      'POST',
      `/apps/${appId}/links`,
      { service_id: serviceId },
    ),

  unlinkService: (appId: string, serviceId: string) =>
    request<void>('DELETE', `/apps/${appId}/links/${serviceId}`),

  // ---- backups ----

  getBackupConfig: () => request<BackupConfig>('GET', '/settings/backup'),

  saveBackupConfig: (input: {
    endpoint: string
    region: string
    bucket: string
    access_key: string
    secret_key: string
    retention_count: number
  }) => request<BackupConfig>('PUT', '/settings/backup', input),

  setBackupSchedule: (serviceId: string, schedule: string, hour: number) =>
    request<{ schedule: string; hour: number }>(
      'PUT',
      `/services/${serviceId}/backup-schedule`,
      { schedule, hour },
    ),

  listStateBackups: () =>
    request<{ backups: StateBackup[]; available: boolean }>(
      'GET',
      '/settings/state-backups',
    ),

  createStateBackup: () =>
    request<StateBackup>('POST', '/settings/state-backups'),

  listBackups: (serviceId: string) =>
    request<{ backups: Backup[]; backupable: boolean }>(
      'GET',
      `/services/${serviceId}/backups`,
    ),

  createBackup: (serviceId: string) =>
    request<Backup>('POST', `/services/${serviceId}/backups`),

  restoreBackup: (serviceId: string, backupId: string) =>
    request<{ restored: string }>(
      'POST',
      `/services/${serviceId}/backups/${backupId}/restore`,
    ),
}

/**
 * Opens an SSE log stream and returns a function that closes it.
 * EventSource cannot send headers, so the token rides as a query param — the
 * server accepts both.
 */
function openStream(
  path: string,
  onLine: (line: LogLine) => void,
  onError?: () => void,
): () => void {
  const token = getToken() ?? ''
  const source = new EventSource(
    `${path}?token=${encodeURIComponent(token)}`,
  )

  source.onmessage = (event) => {
    try {
      onLine(JSON.parse(event.data) as LogLine)
    } catch {
      // A malformed frame is not worth tearing the stream down for.
    }
  }
  source.onerror = () => onError?.()

  return () => source.close()
}

export function streamLogs(
  appId: string,
  onLine: (line: LogLine) => void,
  onError?: () => void,
): () => void {
  return openStream(`/api/v1/apps/${appId}/logs`, onLine, onError)
}

/** Provisioning narration and container output for a service. */
export function streamServiceLogs(
  serviceId: string,
  onLine: (line: LogLine) => void,
  onError?: () => void,
): () => void {
  return openStream(`/api/v1/services/${serviceId}/logs`, onLine, onError)
}
