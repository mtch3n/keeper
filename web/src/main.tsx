import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import './index.css'
import App from './App.tsx'
import { TooltipProvider } from '@/components/ui/tooltip'
import { LiveStatusProvider } from '@/components/wrappers/LiveStatus'
import { ConnectionScopeProvider } from '@/components/wrappers/ConnectionScopeProvider'
import { SessionScopeProvider } from '@/components/wrappers/SessionScopeProvider'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <ConnectionScopeProvider>
        <SessionScopeProvider>
          <LiveStatusProvider>
            <TooltipProvider delay={400}>
              <App />
            </TooltipProvider>
          </LiveStatusProvider>
        </SessionScopeProvider>
      </ConnectionScopeProvider>
    </BrowserRouter>
  </StrictMode>,
)
