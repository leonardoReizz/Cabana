export type Theme = 'light' | 'dark' | 'system'

const KEY = 'cabana.theme'

export function getTheme(): Theme {
  const stored = localStorage.getItem(KEY)
  return stored === 'light' || stored === 'dark' ? stored : 'system'
}

/**
 * Applies the theme by stamping data-theme on <html>, which the token file
 * keys off. 'system' removes the attribute so prefers-color-scheme takes over.
 */
export function applyTheme(theme: Theme): void {
  if (theme === 'system') {
    document.documentElement.removeAttribute('data-theme')
    localStorage.removeItem(KEY)
  } else {
    document.documentElement.setAttribute('data-theme', theme)
    localStorage.setItem(KEY, theme)
  }
}

export function resolveTheme(): 'light' | 'dark' {
  const theme = getTheme()
  if (theme !== 'system') return theme
  return window.matchMedia('(prefers-color-scheme: dark)').matches
    ? 'dark'
    : 'light'
}
