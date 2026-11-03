import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import {
  getCatalog,
  initCatalog,
  updateCatalogColumns,
  type InitProposal,
} from '@/lib/api'
import type { ColumnPolicy, Policy } from '@/lib/types'

const POLICIES: Policy[] = ['allow', 'token', 'redact', 'scan']

/**
 * One connection's catalog: the classification workhorse.
 *
 * It is a table, not a grid of cards: it shows hundreds of columns and is
 * designed for bulk operation over them rather than one-at-a-time editing. Cards
 * for row data is a slop tell in its own right.
 *
 * The backlog is the point of the screen. Until a column is classified it
 * redacts, which is safe and useless, and nobody fixes a backlog they cannot see
 * in one place.
 */
export function CatalogTab({ connId }: { connId: string }) {
  const [entries, setEntries] = useState<Record<string, ColumnPolicy>>({})
  const [unclassified, setUnclassified] = useState<string[]>([])
  const [proposal, setProposal] = useState<InitProposal | null>(null)
  const [filter, setFilter] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)

  const reload = useCallback(async (id: string) => {
    try {
      const cat = await getCatalog(id)
      setEntries(cat.entries)
      setUnclassified(cat.unclassified ?? [])
      setLoaded(true)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    void reload(connId)
  }, [connId, reload])

  const put = async (key: string, policy: ColumnPolicy) => {
    setBusy(true)
    try {
      await updateCatalogColumns(connId, { [key]: policy })
      await reload(connId)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (!loaded && !error) return <Skeleton className="h-40 w-full" />

  const byKey = (a: [string, unknown], b: [string, unknown]) => a[0].localeCompare(b[0])
  const undecided: [string, ColumnPolicy | null][] = unclassified.map((key): [string, null] => [key, null]).sort(byKey)
  const shown = [...undecided, ...Object.entries(entries).sort(byKey)].filter(([key]) => key.includes(filter))

  return (
    <div className="flex flex-col gap-6">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <div className="flex flex-wrap items-end gap-3">
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="filter columns"
          className="w-64"
        />
        <Button
          variant="outline"
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            try {
              setProposal(await initCatalog(connId, 200))
            } catch (e) {
              setError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          Propose classifications
        </Button>
      </div>

      {unclassified.length > 0 ? (
        <p className="text-sm">
          <span className="text-waiting">{unclassified.length} column(s) unclassified.</span> Each one
          redacts until you decide.
        </p>
      ) : null}

      {proposal ? <Proposal proposal={proposal} onApply={put} busy={busy} /> : null}

      <Separator />

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Column</TableHead>
            <TableHead>Policy</TableHead>
            <TableHead>Namespace</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {shown.map(([key, p]) => (
            <CatalogRow key={key} columnKey={key} policy={p} busy={busy} onChange={put} />
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function namespaceFor(columnKey: string) {
  return columnKey.split('.').pop() ?? columnKey
}

function CatalogRow({
  columnKey,
  policy,
  busy,
  onChange,
}: {
  columnKey: string
  policy: ColumnPolicy | null
  busy: boolean
  onChange: (key: string, p: ColumnPolicy) => Promise<void>
}) {
  const [namespace, setNamespace] = useState(policy?.namespace ?? '')
  const [problem, setProblem] = useState<string | null>(null)
  useEffect(() => setNamespace(policy?.namespace ?? ''), [policy?.namespace])

  const choose = (next: Policy) => {
    const entry: ColumnPolicy = { policy: next }
    if (next === 'token') entry.namespace = namespaceFor(columnKey)
    void onChange(columnKey, entry)
  }
  const commitNamespace = () => {
    if (!policy) return
    if (namespace.trim() === '') {
      setProblem('A token column needs a namespace.')
      return
    }
    setProblem(null)
    if (namespace !== policy.namespace) void onChange(columnKey, { ...policy, namespace })
  }

  return (
    <TableRow>
      <TableCell className="text-meta">{columnKey}</TableCell>
      <TableCell>
        <Select value={policy?.policy ?? ''} onValueChange={(v) => choose(v as Policy)} disabled={busy}>
          <SelectTrigger className="w-32" aria-label={`Policy for ${columnKey}`}>
            <SelectValue placeholder="unclassified" />
          </SelectTrigger>
          <SelectContent>
            {POLICIES.map((p) => (
              <SelectItem key={p} value={p}>
                {p}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </TableCell>
      <TableCell>
        {policy?.policy === 'token' ? (
          <div className="flex flex-col gap-1">
            <Input
              aria-label={`Namespace for ${columnKey}`}
              value={namespace}
              disabled={busy}
              onChange={(e) => setNamespace(e.target.value)}
              onBlur={commitNamespace}
              className="w-32"
            />
            {problem ? <p className="text-meta text-blocked">{problem}</p> : null}
          </div>
        ) : (
          <span className="text-meta text-muted-foreground">—</span>
        )}
      </TableCell>
    </TableRow>
  )
}

/**
 * `catalog init`'s output, grouped as the API returns it.
 *
 * The grouping is the whole point. Typed scalars and name-heuristic matches can
 * be accepted without reading; text no rule matched is the review task, because
 * that is where unnamed name and address columns live — `fname`, `surname`,
 * `street2` — and the pattern pass measures 0% against them.
 */
function Proposal({
  proposal,
  onApply,
  busy,
}: {
  proposal: InitProposal
  onApply: (key: string, p: ColumnPolicy) => Promise<void>
  busy: boolean
}) {
  const safe = Object.entries(proposal.safe_to_bulk_accept ?? {})
  const review = Object.entries(proposal.needs_review ?? {})

  return (
    <section className="flex flex-col gap-4">
      <div>
        <h2 className="text-heading">Proposed: safe to accept in bulk ({safe.length})</h2>
        <p className="text-meta text-muted-foreground">
          Typed scalars proposed allow, and name-heuristic matches proposed token.
        </p>
        <Button
          disabled={busy || safe.length === 0}
          onClick={async () => {
            for (const [key, p] of safe) await onApply(key, p)
          }}
        >
          Accept all {safe.length}
        </Button>
      </div>

      <div>
        <h2 className="text-heading">Proposed: needs review ({review.length})</h2>
        <p className="text-meta text-muted-foreground">Free text no rule matched. Review each.</p>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Column</TableHead>
              <TableHead>Proposed</TableHead>
              <TableHead className="text-right">Sample hit rate</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {review.map(([key, p]) => (
              <TableRow key={key}>
                <TableCell className="text-meta">{key}</TableCell>
                <TableCell className="text-meta">{p.policy}</TableCell>
                <TableCell className="text-right text-meta">
                  {proposal.sample_rates?.[key] !== undefined
                    ? `${Math.round(proposal.sample_rates[key] * 100)}%`
                    : '—'}
                </TableCell>
                <TableCell className="text-right">
                  <Button variant="outline" disabled={busy} onClick={() => void onApply(key, p)}>
                    Accept
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  )
}
