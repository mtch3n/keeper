import { useEffect, useState } from 'react'
import { Link, Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/wrappers/AppShell'
import { Toaster } from '@/components/ui/toast'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { buttonVariants } from '@/components/ui/button'
import { ConnectionsPage } from '@/pages/ConnectionsPage'
import { InboxPage } from '@/pages/InboxPage'
import { ActivityPage } from '@/pages/ActivityPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { ConnectionPage } from '@/pages/connection/ConnectionPage'
import { LocalRequestPage, SessionNeeded } from '@/pages/LocalRequestPage'
import { SESSION_NEEDED } from '@/lib/api'

function App() {
  const [sessionNeeded, setSessionNeeded] = useState(false)
  useEffect(() => {
    const on = () => setSessionNeeded(true)
    window.addEventListener(SESSION_NEEDED, on)
    return () => window.removeEventListener(SESSION_NEEDED, on)
  }, [])
  if (sessionNeeded) return <SessionNeeded />
  return (
    <>
      <Toaster />
      <Routes>
        {/* keeper opens on what is waiting for you. */}
        <Route path="/" element={<Navigate to="/inbox" replace />} />

        <Route path="/connections" element={<AppShell section="settings" settingsTab="connections"><ConnectionsPage /></AppShell>} />
        <Route path="/connections/:id" element={<AppShell section="settings" settingsTab="connections"><ConnectionPage tab="overview" /></AppShell>} />
        <Route path="/connections/:id/settings" element={<AppShell section="settings" settingsTab="connections"><ConnectionPage tab="settings" /></AppShell>} />
        <Route path="/connections/:id/catalog" element={<AppShell section="settings" settingsTab="connections"><ConnectionPage tab="catalog" /></AppShell>} />
        <Route path="/inbox" element={<AppShell section="inbox"><InboxPage /></AppShell>} />
        <Route path="/activity" element={<AppShell section="activity"><ActivityPage /></AppShell>} />
        <Route path="/settings" element={<AppShell section="settings" settingsTab="general"><SettingsPage /></AppShell>} />

        {/* The local one-use decision page. No AppShell: this is
            reached from a blocked agent's loopback link, not the app's own nav. */}
        <Route path="/r/:requestId" element={<LocalRequestPage />} />

        <Route
          path="*"
          element={
            <Empty className="min-h-svh">
              <EmptyHeader>
                <EmptyTitle>Page not found</EmptyTitle>
                <EmptyDescription>That route does not exist.</EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Link to="/inbox" className={buttonVariants({ variant: 'outline', size: 'sm' })}>
                  Go to Inbox
                </Link>
              </EmptyContent>
            </Empty>
          }
        />
      </Routes>
    </>
  )
}

export default App
