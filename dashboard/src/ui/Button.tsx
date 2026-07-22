import type { ButtonHTMLAttributes, ReactNode, Ref } from 'react'
import styles from './Button.module.css'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'
type Size = 'sm' | 'md' | 'lg'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  loading?: boolean
  fullWidth?: boolean
  children?: ReactNode
  // Declared and applied explicitly so Radix's `asChild` works. Radix needs a
  // ref on the rendered element to move focus into a dialog and restore it on
  // close — the accessibility we adopt Radix for. Relying on `ref` surviving
  // inside the rest spread would work by accident, and break silently.
  ref?: Ref<HTMLButtonElement>
}

export function Button({
  variant = 'secondary',
  size = 'md',
  loading = false,
  fullWidth = false,
  disabled,
  children,
  className,
  ref,
  ...rest
}: Props) {
  const classes = [
    styles.button,
    styles[variant],
    size !== 'md' && styles[size],
    fullWidth && styles.full,
    className,
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <button
      ref={ref}
      className={classes}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      {loading && <span className={styles.spinner} aria-hidden="true" />}
      {children}
    </button>
  )
}
