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
import { LocalRequestPage } from '@/pages/LocalRequestPage'

function App() {
  return (
    <>
      <Toaster />
      <Routes>
        {/* keeper opens on what is waiting for you. */}
        <Route path="/" element={<Navigate to="/inbox" replace />} />

        <Route path="/connections" element={<AppShell section="settings" settingsTab="connections"><ConnectionsPage /></AppShell>} />
        <Route path="/connections/:id" element={<AppShell section="settings" settingsTab="connections"><ConnectionPage /></AppShell>} />
        <Route path="/connections/:id/:tab" element={<AppShell section="settings" settingsTab="connections"><ConnectionPage /></AppShell>} />
        <Route path="/inbox" element={<AppShell section="inbox"><InboxPage /></AppShell>} />
        <Route path="/activity" element={<AppShell section="activity"><ActivityPage /></AppShell>} />
        <Route path="/settings" element={<AppShell section="settings" settingsTab="general"><SettingsPage /></AppShell>} />

        {/* The local one-use decision page (SPEC R8.7g). No AppShell: this is
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
