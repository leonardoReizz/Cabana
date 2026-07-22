import { z } from 'zod'

/**
 * Form schemas.
 *
 * These mirror the server's rules in internal/domain/domain.go and
 * internal/httpapi/apps.go. They exist to give fast feedback, not to be the
 * authority — the server validates independently and its errors still surface.
 * When a rule changes there, change it here too.
 */

export const loginSchema = z.object({
  username: z.string().min(1, 'Username is required'),
  password: z.string().min(1, 'Password is required'),
})

export type LoginInput = z.infer<typeof loginSchema>

// Mirrors domain.ValidateAppName: the name becomes a DNS label and a
// Kubernetes object name, so the constraints are not cosmetic.
export const appNameSchema = z
  .string()
  .min(3, 'At least 3 characters')
  .max(30, 'At most 30 characters')
  .regex(/^[a-z]/, 'Must start with a lowercase letter')
  .regex(/^[a-z0-9-]+$/, 'Only lowercase letters, digits and hyphens')
  .refine((v) => !v.endsWith('-'), 'Cannot end with a hyphen')

// Mirrors validateRepoURL. https only: private repos over ssh arrive in v0.6.
export const repoURLSchema = z
  .string()
  .min(1, 'Repository URL is required')
  .refine((v) => {
    try {
      return new URL(v).protocol === 'https:'
    } catch {
      return false
    }
  }, 'Must be an https:// URL')
  .refine((v) => {
    try {
      return new URL(v).hostname.length > 0
    } catch {
      return false
    }
  }, 'Must include a host')

export const createAppSchema = z.object({
  name: appNameSchema,
  repo_url: repoURLSchema,
  // Empty is allowed and means "main"; the server applies that default.
  git_ref: z.string().max(255, 'Too long'),
})

export type CreateAppInput = z.infer<typeof createAppSchema>

// Mirrors validateEnvKey. Empty keys are allowed here because a blank row is
// how a user abandons one they added by mistake; they are dropped on save.
export const envKeySchema = z
  .string()
  .refine(
    (v) => v === '' || /^[A-Za-z_][A-Za-z0-9_]*$/.test(v),
    'Letters, digits and underscores only, and cannot start with a digit',
  )

export const envSchema = z.object({
  entries: z.array(
    z.object({
      key: envKeySchema,
      value: z.string(),
    }),
  ),
})

export type EnvInput = z.infer<typeof envSchema>

// Service names follow the same DNS-label rules as app names: they become
// Kubernetes object names and in-cluster hostnames.
// Mirrors catalog.ValidateVersion: Docker's tag grammar. Empty is allowed and
// means the template's pinned default.
export const versionSchema = z
  .string()
  .max(128, 'At most 128 characters')
  .refine(
    (v) => v === '' || /^[a-zA-Z0-9_][a-zA-Z0-9._-]*$/.test(v),
    'Letters, digits, dots, hyphens and underscores; cannot start with a dot or hyphen',
  )

export const createServiceSchema = z.object({
  name: appNameSchema,
  version: versionSchema,
})

export type CreateServiceInput = z.infer<typeof createServiceSchema>

// Mirrors domain.ValidateReplicas and domain.ValidateHealth.
// Numbers arrive already converted: the inputs register with valueAsNumber, so
// zod validates rather than coerces. z.coerce would type its input as unknown,
// which breaks the resolver's inference against the form's field types.
export const settingsSchema = z.object({
  replicas: z
    .number({ error: 'Enter a number' })
    .int('Whole number')
    .min(1, 'At least 1')
    .max(20, 'At most 20'),
  healthType: z.enum(['tcp', 'http']),
  healthPath: z.string(),
  initialDelay: z
    .number({ error: 'Enter a number' })
    .int('Whole number')
    .min(0, 'Cannot be negative')
    .max(600, 'At most 600 seconds'),
  period: z
    .number({ error: 'Enter a number' })
    .int('Whole number')
    .min(1, 'At least 1 second')
    .max(300, 'At most 300 seconds'),
})
  // Only meaningful for http checks, so the rule is conditional rather than a
  // blanket requirement that would reject a valid tcp config.
  .refine((v) => v.healthType !== 'http' || v.healthPath.startsWith('/'), {
    message: 'Path must start with /',
    path: ['healthPath'],
  })

export type SettingsInput = z.infer<typeof settingsSchema>

// Mirrors handlePutBackupConfig. The secret is optional on update — an empty
// value keeps whatever is already stored.
export const backupConfigSchema = z.object({
  endpoint: z
    .string()
    .min(1, 'Required')
    .refine(
      (v) => v.startsWith('http://') || v.startsWith('https://'),
      'Must start with http:// or https://',
    ),
  region: z.string(),
  bucket: z.string().min(1, 'Required'),
  access_key: z.string().min(1, 'Required'),
  secret_key: z.string(),
  retention_count: z
    .number({ error: 'Enter a number' })
    .int('Whole number')
    .min(0, 'Cannot be negative')
    .max(1000, 'At most 1000'),
})

export type BackupConfigInput = z.infer<typeof backupConfigSchema>
