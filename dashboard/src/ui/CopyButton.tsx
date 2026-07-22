import { useEffect, useState } from 'react'
import { Button } from './Button'

/**
 * Copies a value and confirms it did. Credentials are shown once and typed
 * never — without visible confirmation a user cannot tell a successful copy
 * from a silently failed one, and will paste the wrong thing.
 */
export function CopyButton({
  value,
  label = 'Copy',
  size = 'sm',
}: {
  value: string
  label?: string
  size?: 'sm' | 'md'
}) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')

  useEffect(() => {
    if (state === 'idle') return
    const timer = setTimeout(() => setState('idle'), 1600)
    return () => clearTimeout(timer)
  }, [state])

  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setState('copied')
    } catch {
      // The clipboard API needs a secure context, and cabana serves plain HTTP
      // until TLS lands — so this path is reachable, not theoretical.
      setState('failed')
    }
  }

  return (
    <Button
      type="button"
      size={size}
      onClick={() => void copy()}
      aria-live="polite"
    >
      {state === 'copied' ? 'Copied' : state === 'failed' ? 'Select manually' : label}
    </Button>
  )
}
