import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { AuthProvider } from './auth'
import { ConfirmProvider } from './ui/AlertDialog'
import { applyTheme, getTheme } from './theme'
import { App } from './App'
import './styles/base.css'

// Applied before first paint so a dark-theme user never sees a light flash.
applyTheme(getTheme())

const root = document.getElementById('root')
if (!root) throw new Error('#root is missing from index.html')

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <ConfirmProvider>
          <App />
        </ConfirmProvider>
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
)
