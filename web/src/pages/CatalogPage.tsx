import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import {
  getCatalog,
  getCatalogGrantStatements,
  initCatalog,
  listConnections,
  updateCatalogColumns,
  type InitProposal,
} from '@/lib/api'
import type { ColumnPolicy, ConnectionSummary, PartialForm, Policy } from '@/lib/types'

const POLICIES: Policy[] = ['allow', 'scan', 'partial', 'token', 'redact', 'drop']
const FORMS: PartialForm[] = ['email_domain', 'card_bin_last4', 'phone_country_area', 'ip_network']

/**
 * The classification workhorse (SPEC §5, UI.md §2.3).
 *
 * It is a table, not a grid of cards: it shows hundreds of columns and is
 * designed for bulk operation over them rather than one-at-a-time editing. Cards
 * for row data is a slop tell in its own right.
 *
 * The backlog is the point of the screen. Until a column is classified it
 * redacts, which is safe and useless, and nobody fixes a backlog they cannot see
 * in one place.
 */
export function CatalogPage() {
  const [list, setList] = useState<ConnectionSummary[] | null>(null)
  const [connId, setConnId] = useState<string | null>(null)
  const [entries, setEntries] = useState<Record<string, ColumnPolicy>>({})
  const [unclassified, setUnclassified] = useState<string[]>([])
  const [proposal, setProposal] = useState<InitProposal | null>(null)
  const [grants, setGrants] = useState<string[] | null>(null)
  const [filter, setFilter] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    void (async () => {
      try {
        const cs = await listConnections()
        setList(cs)
        if (cs.length > 0) setConnId(cs[0].id)
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      }
    })()
  }, [])

  const reload = useCallback(async (id: string) => {
    try {
      const cat = await getCatalog(id)
      setEntries(cat.entries)
      setUnclassified(cat.unclassified ?? [])
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    if (connId) void reload(connId)
  }, [connId, reload])

  const put = async (key: string, policy: ColumnPolicy) => {
    if (!connId) return
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

  if (list === null) return <Skeleton className="h-40 w-full" />
  if (list.length === 0) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>No connections</EmptyTitle>
          <EmptyDescription>A catalog classifies one database's columns; register one first.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const shown = Object.entries(entries).filter(([key]) => key.includes(filter))

  return (
    <div className="flex flex-col gap-6">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <div className="flex flex-wrap items-end gap-3">
        {list.map((c) => (
          <Button key={c.id} variant={connId === c.id ? 'default' : 'outline'} onClick={() => setConnId(c.id)}>
            {c.name}
          </Button>
        ))}
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="filter columns"
          className="w-64"
        />
        <Button
          variant="outline"
          disabled={busy || !connId}
          onClick={async () => {
            setBusy(true)
            try {
              setProposal(await initCatalog(connId!, 200))
            } catch (e) {
              setError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          Propose classifications
        </Button>
        <Button
          variant="outline"
          disabled={busy || !connId}
          onClick={async () => {
            try {
              setGrants((await getCatalogGrantStatements(connId!)).statements)
            } catch (e) {
              setError(e instanceof Error ? e.message : String(e))
            }
          }}
        >
          Suggested grants
        </Button>
      </div>

      {unclassified.length > 0 ? (
        <p className="text-sm">
          <span className="text-waiting">{unclassified.length} column(s) unclassified.</span> Each one redacts
          until you decide, which is safe and not useful.
        </p>
      ) : null}

      {proposal ? <Proposal proposal={proposal} onApply={put} busy={busy} /> : null}

      {grants ? (
        <section className="flex flex-col gap-2">
          <h2 className="text-heading">Suggested grants</h2>
          <p className="text-meta text-muted-foreground">
            Applying these removes every `drop` column from the role's reach, so keeper never sees them. That
            survives keeper being wrong about everything else.
          </p>
          <pre className="overflow-x-auto text-meta whitespace-pre-wrap">{grants.join('\n')}</pre>
        </section>
      ) : null}

      <Separator />

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Column</TableHead>
            <TableHead>Policy</TableHead>
            <TableHead>Namespace</TableHead>
            <TableHead>Form</TableHead>
            <TableHead>Name</TableHead>
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

function CatalogRow({
  columnKey,
  policy,
  busy,
  onChange,
}: {
  columnKey: string
  policy: ColumnPolicy
  busy: boolean
  onChange: (key: string, p: ColumnPolicy) => Promise<void>
}) {
  const set = (patch: Partial<ColumnPolicy>) => void onChange(columnKey, { ...policy, ...patch })

  return (
    <TableRow>
      <TableCell className="text-meta">{columnKey}</TableCell>
      <TableCell>
        <Select value={policy.policy} onValueChange={(v) => set({ policy: v as Policy })} disabled={busy}>
          <SelectTrigger className="w-32">
            <SelectValue />
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
        {/* R5.2b: a token without a namespace is a validation error, not a
            default. Two token columns that do not share one will not join, which
            is the single thing tokens exist to preserve. */}
        {policy.policy === 'token' ? (
          <Input
            value={policy.namespace ?? ''}
            disabled={busy}
            onBlur={(e) => set({ namespace: e.target.value })}
            onChange={() => undefined}
            defaultValue={policy.namespace ?? ''}
            className="w-32"
          />
        ) : (
          <span className="text-meta text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell>
        {policy.policy === 'partial' ? (
          <Select
            value={policy.form ?? ''}
            onValueChange={(v) => set({ form: v as PartialForm })}
            disabled={busy}
          >
            <SelectTrigger className="w-48">
              <SelectValue placeholder="choose a form" />
            </SelectTrigger>
            <SelectContent>
              {FORMS.map((f) => (
                <SelectItem key={f} value={f}>
                  {f}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <span className="text-meta text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell>
        <label className="flex items-center gap-2 text-meta">
          <Checkbox
            checked={policy.hide_name ?? false}
            disabled={busy}
            onCheckedChange={(v) => set({ hide_name: v === true })}
          />
          hidden
        </label>
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
        <p className="text-meta text-muted-foreground">
          Free text no rule matched. A name or an address here is invisible to the pattern pass, so each is
          accepted on its own.
        </p>
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
