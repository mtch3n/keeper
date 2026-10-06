import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import './index.css'
import App from './App.tsx'
import { TooltipProvider } from '@/components/ui/tooltip'
import { LiveStatusProvider } from '@/components/wrappers/LiveStatus'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
          <LiveStatusProvider>
            <TooltipProvider delay={400}>
              <App />
            </TooltipProvider>
          </LiveStatusProvider>
    </BrowserRouter>
  </StrictMode>,
)
