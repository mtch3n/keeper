import { Link, Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/wrappers/AppShell'
import { Toaster } from '@/components/ui/toast'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { buttonVariants } from '@/components/ui/button'
import { ConnectionsPage } from '@/pages/ConnectionsPage'
import { ApprovalsPage } from '@/pages/ApprovalsPage'
import { PermissionsPage } from '@/pages/PermissionsPage'
import { ActivityPage } from '@/pages/ActivityPage'
import { CatalogPage } from '@/pages/CatalogPage'
import { PolicyPage } from '@/pages/PolicyPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { LocalRequestPage } from '@/pages/LocalRequestPage'

function App() {
  return (
    <>
      <Toaster />
      <Routes>
        {/* `/keeper` with no argument opens the UI itself (UI.md §6); there is
            no list to land on, so the working scope is Connections. */}
        <Route path="/" element={<Navigate to="/connections" replace />} />

        <Route path="/connections" element={<AppShell section="connections"><ConnectionsPage /></AppShell>} />
        <Route path="/approvals" element={<AppShell section="approvals"><ApprovalsPage /></AppShell>} />
        <Route path="/permissions" element={<AppShell section="permissions"><PermissionsPage /></AppShell>} />
        <Route path="/activity" element={<AppShell section="activity"><ActivityPage /></AppShell>} />
        <Route path="/catalog" element={<AppShell section="catalog"><CatalogPage /></AppShell>} />
        <Route path="/policy" element={<AppShell section="policy"><PolicyPage /></AppShell>} />
        <Route path="/settings" element={<AppShell section="settings"><SettingsPage /></AppShell>} />

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
                <Link to="/connections" className={buttonVariants({ variant: 'outline', size: 'sm' })}>
                  Go to Connections
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
