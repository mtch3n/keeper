import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { listGrants, revokeGrant, subscribeToEvents } from '@/lib/api'
import { age } from '@/lib/render'
import type { Grant } from '@/lib/types'

/**
 * The permission list (SPEC §9.3, UI.md §2.4).
 *
 * Its job is the list, not the click. A prompt shows one decision at a time and
 * shows nothing about what was granted last week, and that asymmetry is what
 * turns a permission system into a drawer nobody opens. So this screen is a flat
 * table of every entry, with the two columns that decide whether an entry should
 * still exist: when it was last used, and how often.
 *
 * There is no tree, no wildcard and no select-all anywhere on it. R9.3c grants
 * exactly the path named — a schema does not cover its relations, a view does not
 * cover its base tables — and a control that granted a subtree would be the
 * rule's inverse however convenient it looked on a schema with four hundred
 * relations.
 */
export function PermissionsPage() {
  const [grants, setGrants] = useState<Grant[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      setGrants(await listGrants())
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    void refresh()
    return subscribeToEvents((event) => {
      if (event.kind === 'approval' || event.kind === 'session') void refresh()
    })
  }, [refresh])

  if (grants === null) return <Skeleton className="h-40 w-full" />

  if (grants.length === 0) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>No allow rules</EmptyTitle>
          <EmptyDescription>
            Approving an escalation can leave a rule here so keeper stops asking the same question. Each one
            covers exactly the path it names.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  // Session grants first: they are the narrower kind and they die with the
  // session, so they belong at the top where their impermanence reads.
  const ordered = [...grants].sort((a, b) => {
    if (a.lifetime !== b.lifetime) return a.lifetime === 'session' ? -1 : 1
    return a.created_at.localeCompare(b.created_at)
  })

  return (
    <div className="flex flex-col gap-6">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Path</TableHead>
            <TableHead>Lifetime</TableHead>
            <TableHead className="text-right">Ceiling</TableHead>
            <TableHead className="text-right">Granted</TableHead>
            <TableHead className="text-right">Last used</TableHead>
            <TableHead className="text-right">Uses</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {ordered.map((g) => (
            <TableRow key={g.id}>
              <TableCell className="text-meta">
                read({g.path.connection_id}:{g.path.relation.schema}.{g.path.relation.relation})
              </TableCell>
              <TableCell className="text-meta">
                {g.lifetime}
                {g.suspended ? (
                  <span className="ml-2 text-waiting" title={g.reason ?? 'a schema change touched this relation'}>
                    suspended
                  </span>
                ) : null}
              </TableCell>
              <TableCell className="text-right text-meta">{g.row_ceiling}</TableCell>
              <TableCell className="text-right text-meta">{age(g.created_at)} ago</TableCell>
              {/* An entry granted in March and never used since is the one to
                  revoke, and nothing else in the product can say so. */}
              <TableCell className="text-right text-meta">
                {g.last_used_at ? `${age(g.last_used_at)} ago` : 'never'}
              </TableCell>
              <TableCell className="text-right text-meta">{g.uses}</TableCell>
              <TableCell className="text-right">
                <Button
                  variant="outline"
                  onClick={async () => {
                    try {
                      await revokeGrant(g.id)
                      await refresh()
                    } catch (e) {
                      setError(e instanceof Error ? e.message : String(e))
                    }
                  }}
                >
                  Revoke
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <p className="text-meta text-muted-foreground">
        A rule covers exactly the path it names. A join reaching one relation you granted and one you did not
        still escalates, and a view is its own path even when its base table is granted.
      </p>
    </div>
  )
}
