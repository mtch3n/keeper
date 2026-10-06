import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { getCatalog, listHosts, removeConnection, type ConnectionDetail } from '@/lib/api'
import { auditedAge } from '@/lib/render'
import type { HostView } from '@/lib/types'
import { tabPath } from '@/pages/connection/tabs'

/**
 * A connection in one screen: where it is, who it connects as, and every count
 * that asks for something, each a link to the tab that acts on it. It answers
 * "what state is this database in" without visiting the other tabs.
 */
export function OverviewTab({ detail, onError }: { detail: ConnectionDetail; onError: (e: string) => void }) {
  const [host, setHost] = useState<HostView | null>(null)
  // null while loading; 'unavailable' when the catalog could not be read, which
  // is that row's state and not the page's.
  const [unclassified, setUnclassified] = useState<number | 'unavailable' | null>(null)
  const navigate = useNavigate()
  const id = detail.id

  useEffect(() => {
    listHosts()
      .then((hosts) => setHost(hosts.find((h) => h.connections.includes(id)) ?? null))
      .catch((e) => onError(e instanceof Error ? e.message : String(e)))
    getCatalog(id)
      .then((cat) => setUnclassified(cat.unclassified_count ?? cat.unclassified?.length ?? 0))
      .catch(() => setUnclassified('unavailable'))
  }, [id, onError])

  const findings = detail.audited_privileges.findings?.length ?? 0
  const never = auditedAge(detail.audited_privileges.audited_at) === 'never'
  const stages = detail.detection ?? []
  const link = 'underline underline-offset-4 hover:text-foreground'

  return (
    <div className="flex flex-col gap-8">
      <Facts>
        <Fact label="host">{host ? `${host.name} · ${host.address}:${host.port} · sslmode ${host.sslmode}` : '—'}</Fact>
        <Fact label="database">{detail.database}</Fact>
        <Fact label="username">{detail.username}</Fact>
        <Fact label="mode">
          <Link to={tabPath(id, 'limits')} className={link}>
            {detail.mode}
          </Link>
        </Fact>
        <Fact label="detection">
          <Link to={tabPath(id, 'detection')} className={link}>
            {stages.length === 0 ? 'off — free text is redacted whole' : stages.map((s) => s.kind).join(' → ')}
          </Link>
        </Fact>
        <Fact label="catalog">
          {unclassified === null ? (
            '…'
          ) : unclassified === 'unavailable' ? (
            <span className="text-muted-foreground">unavailable — the catalog could not be read from the database</span>
          ) : (
            <Link to={tabPath(id, 'catalog')} className={link}>
              {unclassified} unclassified columns
            </Link>
          )}
        </Fact>
        <Fact label="privileges">
          {never ? (
            <Link to={tabPath(id, 'privileges')} className={link}>
              not audited yet
            </Link>
          ) : (
            <Link to={tabPath(id, 'privileges')} className={link}>
              {findings} privilege findings
            </Link>
          )}
        </Fact>
        <Fact label="writes">
          <Link to={tabPath(id, 'limits')} className={link}>
            {detail.writes === 'approve' ? 'allowed, each one approved in the Inbox' : 'off — read-only'}
          </Link>
        </Fact>
      </Facts>

      <Separator />

      <div className="flex">
        <RemoveConnection
          id={id}
          name={detail.name}
          onRemoved={async () => navigate('/connections')}
          onError={onError}
        />
      </div>
    </div>
  )
}

/**
 * Removing a connection takes its credential and every allow rule naming it. The name is retyped rather than confirmed with a click,
 * because a grant naming a connection that no longer exists is a rule nobody
 * can read and nobody can revoke.
 *
 * The catalog file stays. It lives in the project repo and is reviewed like
 * code; deleting it is not this button's business.
 */
export function RemoveConnection({
  id,
  name,
  onRemoved,
  onError,
}: {
  id: string
  name: string
  onRemoved: () => Promise<void>
  onError: (e: string) => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)

  if (!confirming) {
    return (
      <Button variant="outline" onClick={() => setConfirming(true)}>
        Remove
      </Button>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <span className="text-meta text-muted-foreground">retype {name} to remove it</span>
      <div className="flex gap-2">
        <Input value={typed} onChange={(e) => setTyped(e.target.value)} className="w-40" />
        <Button
          disabled={busy || typed !== name}
          onClick={async () => {
            setBusy(true)
            try {
              await removeConnection(id)
              await onRemoved()
            } catch (e) {
              onError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          Remove
        </Button>
        <Button variant="outline" onClick={() => setConfirming(false)}>
          Cancel
        </Button>
      </div>
      <span className="text-meta text-muted-foreground">
        Its credential and every allow rule naming it go with it. The catalog file stays.
      </span>
    </div>
  )
}
