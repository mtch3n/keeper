import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import {
  auditConnection,
  setDenylist,
  setTerms,
  updateConnection,
  type ConnectionDetail,
  type Pattern,
} from '@/lib/api'
import { auditedAge } from '@/lib/render'
import type { Finding, Mode, RelationRef, Stage, StageKind, Writes } from '@/lib/types'

/**
 * What this connection's role can do beyond reading (SPEC R4.1), read where
 * the connection's other controls are set. None of it stops the connection:
 * each finding carries the statement that would narrow it, and keeper never
 * runs that statement — it holds the credential the report is about, and a
 * tool that can narrow its own grants is a tool that can widen them.
 */
export function Privileges({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => Promise<void>
  onError: (e: string) => void
}) {
  const [busy, setBusy] = useState(false)
  const audited = detail.audited_privileges
  const findings = audited.findings ?? []
  const never = auditedAge(audited.audited_at) === 'never'

  const rerun = async () => {
    setBusy(true)
    try {
      await auditConnection(detail.id)
      await onChanged()
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="privileges-heading" className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h2 id="privileges-heading" className="text-heading">
            Privileges
          </h2>
          <span className="text-meta text-muted-foreground">
            {detail.username} on {detail.database} · audited {auditedAge(audited.audited_at)}
          </span>
        </div>
        <Button variant="outline" disabled={busy} onClick={() => void rerun()}>
          {busy ? 'Re-auditing…' : 'Re-audit'}
        </Button>
      </div>
      {never ? (
        <p className="text-sm">
          No report yet: the audit has not run against this role. That says nothing about what it can do.
        </p>
      ) : findings.length === 0 ? (
        <p className="text-sm">This role holds nothing keeper would report.</p>
      ) : (
        <FindingGroups findings={findings} />
      )}
    </section>
  )
}

/** What each kind of finding means, said once for its group. */
const KIND_MEANING: Record<string, string> = {
  attribute: 'Role attributes beyond reading, such as superuser.',
  membership: 'Membership in built-in roles that read files or other roles\' data.',
  'relation-write': 'Tables and views this role can change.',
  'schema-create': 'Schemas this role can create objects in.',
  'function-exec': 'Functions that read or write files, or run programs.',
  'security-definer': 'Functions that run with their owner\'s rights.',
}

type FindingGroup = { key: string; title: string; meaning?: string; findings: Finding[]; script: string }

/**
 * Findings grouped by kind, rarest first, because the one unusual grant is the
 * one to read and forty identical REVOKEs are one decision, not forty. Each
 * group's statements are shown in full as one block — a statement you have to
 * click to see is one you will not paste — and Copy all fixes joins every
 * group into one transaction. keeper never runs any of it: it holds the
 * credential the report is about, and a tool that can narrow its own grants is
 * a tool that can widen them.
 */
function groupFindings(findings: Finding[]): FindingGroup[] {
  const byKind = new Map<string, Finding[]>()
  const loose: Finding[] = []
  for (const f of findings) {
    if (!f.narrower) {
      loose.push(f)
      continue
    }
    byKind.set(f.kind, [...(byKind.get(f.kind) ?? []), f])
  }
  const groups: FindingGroup[] = [...byKind.entries()]
    .sort(([a, x], [b, y]) => x.length - y.length || a.localeCompare(b))
    .map(([kind, fs]) => ({
      key: kind,
      title: `${kind} · ${fs.length}`,
      meaning: KIND_MEANING[kind],
      findings: fs,
      script: fs.map((f) => f.narrower).join('\n'),
    }))
  if (loose.length > 0) {
    groups.push({ key: 'loose', title: `No single statement removes these · ${loose.length}`, findings: loose, script: '' })
  }
  return groups
}

function FindingGroups({ findings }: { findings: Finding[] }) {
  const groups = groupFindings(findings)
  const all = groups.filter((g) => g.script !== '')
  const [copied, setCopied] = useState<string | null>(null)
  const copy = async (key: string, text: string) => {
    await navigator.clipboard.writeText(text)
    setCopied(key)
  }

  return (
    <div className="flex flex-col gap-8">
      {all.length > 0 ? (
        <div className="flex flex-wrap items-center gap-3">
          <Button onClick={() => void copy('all', `BEGIN;\n${all.map((g) => g.script).join('\n')}\nCOMMIT;\n`)}>
            Copy all fixes
          </Button>
          <span className="text-meta text-muted-foreground" aria-live="polite">
            {copied === 'all' ? 'Copied — run it as an owner of these objects; keeper never runs it.' : ''}
          </span>
        </div>
      ) : null}
      {groups.map((g) => (
        <section key={g.key} className="flex min-w-0 flex-col gap-3">
          <div className="flex flex-wrap items-baseline justify-between gap-3">
            <h3 className="text-label">{g.title}</h3>
            {g.script ? (
              <Button variant="outline" size="sm" onClick={() => void copy(g.key, g.script)}>
                {copied === g.key ? 'Copied' : 'Copy'}
              </Button>
            ) : null}
          </div>
          {g.meaning ? <p className="text-sm text-muted-foreground">{g.meaning}</p> : null}
          {g.key === 'loose' ? (
            <p className="text-sm text-muted-foreground">
              They may be vendor-owned objects you cannot revoke on. Read each and decide.
            </p>
          ) : null}
          <ul className="flex flex-col gap-1">
            {g.findings.map((f) => (
              <li key={f.id} className="text-sm">
                {f.detail}
              </li>
            ))}
          </ul>
          {g.script ? (
            <pre aria-label={`${g.key} statements`} className="overflow-x-auto text-meta whitespace-pre-wrap">
              {g.findings.map((f) => (
                <span key={f.id} className="block">
                  {f.narrower}
                </span>
              ))}
            </pre>
          ) : null}
        </section>
      ))}
    </div>
  )
}

const MODES: { value: Mode; title: string; consequence: string }[] = [
  {
    value: 'strict',
    title: 'Strict',
    consequence:
      'Known-safe reads run. A read near the row ceiling, or through a view whose definition changed, waits for you to approve it.',
  },
  {
    value: 'assisted',
    title: 'Assisted',
    consequence:
      'Ordinary reads run, and so do those uncertain ones, under the same masking. Values keeper cannot resolve are masked, never released.',
  },
]

const WRITES: { value: Writes; title: string; consequence: string }[] = [
  {
    value: 'off',
    title: 'Read-only',
    consequence: 'Its sessions are read-only at the server. A write is refused at once.',
  },
  {
    value: 'approve',
    title: 'Writes with approval',
    consequence: 'An agent may send INSERT, UPDATE and DELETE. Each waits in the Inbox showing the rows it changes.',
  },
]

/**
 * Mode is text with a sentence of consequence under each option, never a
 * coloured pill or a slider. The two differ in who decides an uncertain read —
 * keeper's masking or a human — and a control that rendered that as a position
 * on a scale would be lying about what it does.
 */
export function ModeSelector({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [busy, setBusy] = useState(false)

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Mode</h2>
      <RadioGroup
        value={detail.mode}
        onValueChange={async (value) => {
          setBusy(true)
          try {
            await updateConnection(detail.id, { mode: value as Mode })
            onChanged()
          } catch (e) {
            onError(e instanceof Error ? e.message : String(e))
          } finally {
            setBusy(false)
          }
        }}
        className="flex flex-col gap-4"
      >
        {MODES.map((m) => (
          <label key={m.value} className="flex gap-4">
            <RadioGroupItem value={m.value} disabled={busy} className="mt-1" />
            <span className="flex flex-col">
              <span className="text-sm">{m.title}</span>
              <span className="text-meta text-muted-foreground">{m.consequence}</span>
            </span>
          </label>
        ))}
      </RadioGroup>
      <p className="text-meta text-muted-foreground">
        A mode never allows a write and never authorizes a disclosure. Executing a query, disclosing cleartext and
        modifying the database are three separate grants.
      </p>
    </section>
  )
}

export function LimitsForm({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [maxRows, setMaxRows] = useState(String(detail.limits.max_rows_ceiling))
  const [maxKiB, setMaxKiB] = useState(String(Math.round(detail.limits.max_bytes / 1024)))
  const [timeout, setTimeoutMs] = useState(String(Math.round(detail.limits.statement_timeout / 1e6)))
  const [scanSample, setScanSample] = useState(String(detail.limits.scan_sample))
  const [busy, setBusy] = useState(false)

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Limits</h2>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">row ceiling</span>
          <Input value={maxRows} onChange={(e) => setMaxRows(e.target.value)} className="w-32" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">size cap (KiB)</span>
          <Input value={maxKiB} onChange={(e) => setMaxKiB(e.target.value)} className="w-32" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">statement timeout (ms)</span>
          <Input value={timeout} onChange={(e) => setTimeoutMs(e.target.value)} className="w-40" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">scan sample (rows)</span>
          <Input value={scanSample} onChange={(e) => setScanSample(e.target.value)} className="w-32" />
        </label>
        <Button
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            try {
              await updateConnection(detail.id, {
                limits: {
                  max_rows_ceiling: Number(maxRows),
                  max_bytes: Number(maxKiB) * 1024,
                  statement_timeout: Number(timeout) * 1e6,
                  scan_sample: Number(scanSample),
                },
              })
              onChanged()
            } catch (e) {
              onError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          Save
        </Button>
      </div>
      <p className="text-meta text-muted-foreground">
        The row ceiling is a privacy control, not a performance one: every row returned is a row sent to a third
        party. An agent can ask for fewer and never for more. The size cap stops one wide column from filling an
        agent's context under the row ceiling; a result it cuts says so.
      </p>
    </section>
  )
}

const KINDS: { value: StageKind; title: string; finds: string }[] = [
  {
    value: 'patterns',
    title: 'Patterns',
    finds:
      'Emails, card numbers, IBANs, SSNs, phone numbers, IPs and national IDs, by pattern and checksum, in process. It does not find names or addresses.',
  },
  {
    value: 'list',
    title: 'Your list',
    finds: 'The deny terms and expressions below — names, codenames, your own identifiers.',
  },
]

/** "label = expr" or a bare expression, one per line. */
function parsePatterns(text: string): Pattern[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l !== '')
    .map((l) => {
      const m = /^([a-z0-9_]+)\s*=\s*(.+)$/.exec(l)
      return m ? { label: m[1], expr: m[2] } : { expr: l }
    })
}

/**
 * The pipeline a `scan` column's free text runs through, in order. Each stage
 * gets the text with earlier stages' hits masked, and a text earlier hits
 * cover entirely is not sent on. Detection never sets a column's policy: the
 * catalog does. With no stage on, every scan cell is redacted whole.
 *
 * Terms and expressions are write-only. The daemon answers a save with counts
 * and never sends one back, so these boxes start empty and a save states the
 * whole set.
 */
export function DetectionEditor({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [busy, setBusy] = useState(false)
  const [deny, setDeny] = useState('')
  const [allow, setAllow] = useState('')
  const [patterns, setPatterns] = useState('')
  const [entities, setEntities] = useState<Record<string, string>>(() =>
    Object.fromEntries((detail.detection ?? []).map((st) => [st.kind, (st.entities ?? []).join(', ')])),
  )
  const [saved, setSaved] = useState<{ deny: number; allow: number; patterns: number } | null>(null)
  const stages = detail.detection ?? []
  const on = (k: StageKind) => stages.some((st) => st.kind === k)

  const save = async (next: Stage[]) => {
    setBusy(true)
    try {
      await updateConnection(detail.id, { detection: next })
      onChanged()
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const stageFor = (k: StageKind): Stage => {
    const list = (entities[k] ?? '')
      .split(',')
      .map((e) => e.trim())
      .filter((e) => e !== '')
    const prev = stages.find((st) => st.kind === k)
    return { kind: k, ...(list.length > 0 ? { entities: list } : {}), ...(prev?.raw ? { raw: true } : {}) }
  }
  const toggle = (k: StageKind, enabled: boolean) =>
    void save(KINDS.map((x) => x.value).filter((x) => (x === k ? enabled : on(x))).map(stageFor))
  const applyEntities = () => void save(KINDS.map((x) => x.value).filter(on).map(stageFor))

  const lines = (s: string) =>
    s
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l !== '')

  const saveTerms = async () => {
    setBusy(true)
    try {
      setSaved(await setTerms(detail.id, { deny: lines(deny), allow: lines(allow), patterns: parsePatterns(patterns) }))
      setDeny('')
      setAllow('')
      setPatterns('')
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Detection</h2>
      <FieldGroup>
        {KINDS.map((k) => (
          <Field key={k.value}>
            <Field orientation="horizontal">
              <Switch
                id={`stage-${k.value}`}
                checked={on(k.value)}
                disabled={busy}
                onCheckedChange={(v) => toggle(k.value, v)}
              />
              <FieldContent>
                <FieldLabel htmlFor={`stage-${k.value}`}>{k.title}</FieldLabel>
                <FieldDescription>{k.finds}</FieldDescription>
              </FieldContent>
            </Field>
            {on(k.value) ? (
              <div className="flex items-end gap-3">
                <Field>
                  <FieldLabel htmlFor={`entities-${k.value}`}>Only these entities (optional)</FieldLabel>
                  <Input
                    id={`entities-${k.value}`}
                    value={entities[k.value] ?? ''}
                    onChange={(e) => setEntities((m) => ({ ...m, [k.value]: e.target.value }))}
                    placeholder="email_address, credit_card"
                    className="w-80"
                  />
                </Field>
                <Button variant="outline" disabled={busy} onClick={applyEntities}>
                  Apply
                </Button>
              </div>
            ) : null}
          </Field>
        ))}
      </FieldGroup>
      {stages.length === 0 ? (
        <p className="text-meta text-muted-foreground">
          Off: free text is not examined, so every scan cell is redacted whole.
        </p>
      ) : null}

      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="terms-deny">Deny terms, one per line</FieldLabel>
          <Textarea id="terms-deny" value={deny} onChange={(e) => setDeny(e.target.value)} disabled={busy} />
        </Field>
        <Field>
          <FieldLabel htmlFor="terms-patterns">Expressions, one per line as label = expression</FieldLabel>
          <Textarea
            id="terms-patterns"
            value={patterns}
            onChange={(e) => setPatterns(e.target.value)}
            disabled={busy}
            placeholder="employee_id = EMP-\d{6}"
          />
          <FieldDescription>
            Go RE2 syntax, so no expression can stall a query. One that does not compile or matches empty text is
            refused by name.
          </FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="terms-allow">Allow terms, one per line</FieldLabel>
          <Textarea id="terms-allow" value={allow} onChange={(e) => setAllow(e.target.value)} disabled={busy} />
          <FieldDescription>
            A value any stage finds is left visible only when it equals an allow term exactly, ignoring case.
          </FieldDescription>
        </Field>
      </FieldGroup>
      <div className="flex items-center gap-3">
        <Button disabled={busy} onClick={() => void saveTerms()}>
          Replace terms
        </Button>
        {saved ? (
          <span className="text-meta text-muted-foreground">
            stored {saved.deny} deny term(s), {saved.patterns} expression(s), {saved.allow} allow term(s)
          </span>
        ) : null}
      </div>
      <p className="text-meta text-muted-foreground">
        Never shown again once saved. Saving replaces the whole set, so an empty save clears it.
      </p>
    </section>
  )
}

export function DenylistEditor({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [entry, setEntry] = useState('')
  const [busy, setBusy] = useState(false)
  const current = detail.denylist ?? []

  const save = async (next: RelationRef[]) => {
    setBusy(true)
    try {
      await setDenylist(detail.id, next)
      onChanged()
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Denylist</h2>
      {current.length === 0 ? (
        <p className="text-meta text-muted-foreground">nothing denied</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Relation</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {current.map((r) => (
              <TableRow key={`${r.schema}.${r.relation}`}>
                <TableCell className="text-meta">
                  {r.schema}.{r.relation}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() =>
                      void save(current.filter((x) => !(x.schema === r.schema && x.relation === r.relation)))
                    }
                  >
                    Remove
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <div className="flex items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">schema.relation</span>
          <Input value={entry} onChange={(e) => setEntry(e.target.value)} className="w-64" />
        </label>
        <Button
          disabled={busy || !entry.includes('.')}
          onClick={() => {
            const [schema, ...rest] = entry.split('.')
            void save([...current, { schema, relation: rest.join('.') }])
            setEntry('')
          }}
        >
          Deny
        </Button>
      </div>
      <p className="text-meta text-muted-foreground">
        Evaluated against the plan, so a denied base table is caught even when the statement only names a view
        over it. No approval overrides it — remove the entry instead, which is a visible edit rather than a click
        inside a prompt. It is a convenience, not a boundary: the boundary is a grant the role never had.
      </p>
    </section>
  )
}

/**
 * Whether an agent that declares persistent token scope gets tokens that
 * outlive its session. Off by default: a pseudonym that lives for months links
 * records across all of them.
 */
export function TokensSetting({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [busy, setBusy] = useState(false)
  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Tokens</h2>
      <Field orientation="horizontal">
        <Switch
          id="persistent-tokens"
          checked={detail.persistent_tokens}
          disabled={busy}
          onCheckedChange={async (on) => {
            setBusy(true)
            try {
              await updateConnection(detail.id, { persistent_tokens: on })
              onChanged()
            } catch (e) {
              onError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        />
        <FieldContent>
          <FieldLabel htmlFor="persistent-tokens">Allow persistent tokens</FieldLabel>
          <FieldDescription>
            Off, every agent gets session tokens, which resolve only in the session that saw them. On, an agent that
            declares persistent scope gets tokens that mean the same value in every session and on every machine
            holding this vault.
          </FieldDescription>
        </FieldContent>
      </Field>
    </section>
  )
}

/**
 * Whether this profile's sessions may write, as text with a sentence of
 * consequence under each option, like Mode. Allowing writes never approves
 * one: each still waits in the Inbox.
 */
export function WritesSelector({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [busy, setBusy] = useState(false)

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Writes</h2>
      <RadioGroup
        value={detail.writes}
        onValueChange={async (value) => {
          setBusy(true)
          try {
            await updateConnection(detail.id, { writes: value as Writes })
            onChanged()
          } catch (e) {
            onError(e instanceof Error ? e.message : String(e))
          } finally {
            setBusy(false)
          }
        }}
        className="flex flex-col gap-4"
      >
        {WRITES.map((w) => (
          <label key={w.value} className="flex gap-4">
            <RadioGroupItem value={w.value} disabled={busy} className="mt-1" />
            <span className="flex flex-col">
              <span className="text-sm">{w.title}</span>
              <span className="text-meta text-muted-foreground">{w.consequence}</span>
            </span>
          </label>
        ))}
      </RadioGroup>
      <p className="text-meta text-muted-foreground">
        keeper never switches profiles. A write sent on a read-only profile is refused, naming the profiles on this
        database that allow writes, and the agent resubmits on one of them.
      </p>
    </section>
  )
}
