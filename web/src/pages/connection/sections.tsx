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
import { positiveInt, useSave } from '@/lib/save'
import { ConfirmWiden, SettingSection } from '@/components/wrappers/SettingSection'
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

/** A choice rendered as text with its consequence under it. */
function Choices<T extends string>({
  value,
  options,
  disabled,
  onChoose,
}: {
  value: T
  options: { value: T; title: string; consequence: string }[]
  disabled: boolean
  onChoose: (v: T) => void
}) {
  return (
    <RadioGroup value={value} onValueChange={(v) => onChoose(v as T)} className="flex flex-col gap-4">
      {options.map((o) => (
        <label key={o.value} className="flex gap-4">
          <RadioGroupItem value={o.value} disabled={disabled} className="mt-1" />
          <span className="flex flex-col">
            <span className="text-sm">{o.title}</span>
            <span className="text-sm text-muted-foreground">{o.consequence}</span>
          </span>
        </label>
      ))}
    </RadioGroup>
  )
}

/**
 * Mode is text with a sentence of consequence under each option, never a
 * coloured pill or a slider. The two differ in who decides an uncertain read —
 * keeper's masking or a human — and a control that rendered that as a position
 * on a scale would be lying about what it does.
 */
export function ModeSelector({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  const save = useSave()
  return (
    <SettingSection
      title="Mode"
      save={save}
      why="A mode never allows a write and never authorizes a disclosure. Executing a query, disclosing cleartext and modifying the database are three separate grants."
    >
      <Choices
        value={detail.mode}
        options={MODES}
        disabled={save.busy}
        onChoose={(mode) => void save.run(() => updateConnection(detail.id, { mode })).then((ok) => ok && onChanged())}
      />
    </SettingSection>
  )
}

/**
 * Whether this profile's sessions may write. Allowing writes never approves
 * one: each still waits in the Inbox. Widening asks first; narrowing does not.
 */
export function WritesSelector({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  const save = useSave()
  const [confirming, setConfirming] = useState(false)
  const apply = (writes: Writes) =>
    void save.run(() => updateConnection(detail.id, { writes })).then((ok) => ok && onChanged())

  return (
    <SettingSection
      title="Writes"
      save={save}
      why="keeper never switches profiles. A write sent on a read-only profile is refused, naming the profiles on this database that allow writes, and the agent resubmits on one of them."
    >
      <Choices
        value={detail.writes}
        options={WRITES}
        disabled={save.busy}
        onChoose={(w) => (w === 'approve' && detail.writes !== 'approve' ? setConfirming(true) : apply(w))}
      />
      <ConfirmWiden
        open={confirming}
        title={`Allow writes as ${detail.username}?`}
        description="Agents on this profile may then send INSERT, UPDATE and DELETE. Each still waits in the Inbox for you, showing the rows it changes."
        action="Allow writes"
        onConfirm={() => {
          setConfirming(false)
          apply('approve')
        }}
        onCancel={() => setConfirming(false)}
      />
    </SettingSection>
  )
}

/**
 * Whether an agent that declares persistent token scope gets tokens that
 * outlive its session. Off by default: a pseudonym that lives for months links
 * records across all of them, so turning it on asks first.
 */
export function TokensSetting({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  const save = useSave()
  const [confirming, setConfirming] = useState(false)
  const apply = (on: boolean) =>
    void save.run(() => updateConnection(detail.id, { persistent_tokens: on })).then((ok) => ok && onChanged())

  return (
    <SettingSection title="Tokens" save={save}>
      <Field orientation="horizontal">
        <Switch
          id="persistent-tokens"
          checked={detail.persistent_tokens}
          disabled={save.busy}
          onCheckedChange={(on) => (on ? setConfirming(true) : apply(false))}
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
      <ConfirmWiden
        open={confirming}
        title="Allow persistent tokens?"
        description="A persistent token means the same value for as long as this connection's key lives, so records an agent sees months apart can be linked."
        action="Allow persistent tokens"
        onConfirm={() => {
          setConfirming(false)
          apply(true)
        }}
        onCancel={() => setConfirming(false)}
      />
    </SettingSection>
  )
}

/** One limit: a positive whole number, validated as it is typed. */
function LimitField({
  id,
  label,
  value,
  onChange,
}: {
  id: string
  label: string
  value: string
  onChange: (v: string) => void
}) {
  const invalid = positiveInt(value) === null
  return (
    <Field data-invalid={invalid || undefined} className="w-40">
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input id={id} inputMode="numeric" value={value} aria-invalid={invalid} onChange={(e) => onChange(e.target.value)} />
      {invalid ? <FieldDescription className="text-blocked">Enter a whole number above 0.</FieldDescription> : null}
    </Field>
  )
}

export function LimitsForm({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  const initial = {
    rows: String(detail.limits.max_rows_ceiling),
    kib: String(Math.round(detail.limits.max_bytes / 1024)),
    timeout: String(Math.round(detail.limits.statement_timeout / 1e6)),
    sample: String(detail.limits.scan_sample),
  }
  const [v, setV] = useState(initial)
  const save = useSave()
  const set = (k: keyof typeof initial) => (value: string) => setV((prev) => ({ ...prev, [k]: value }))
  const parsed = {
    rows: positiveInt(v.rows),
    kib: positiveInt(v.kib),
    timeout: positiveInt(v.timeout),
    sample: positiveInt(v.sample),
  }
  const valid = Object.values(parsed).every((n) => n !== null)
  const changed = (Object.keys(initial) as (keyof typeof initial)[]).some((k) => v[k].trim() !== initial[k])

  return (
    <SettingSection
      title="Limits"
      save={save}
      why="The row ceiling is a privacy control, not a performance one: every row returned is a row sent to a third party. An agent can ask for fewer and never for more. The size cap stops one wide column from filling an agent's context under the row ceiling; a result it cuts says so."
    >
      <div className="flex flex-wrap items-start gap-4">
        <LimitField id="limit-rows" label="Row ceiling" value={v.rows} onChange={set('rows')} />
        <LimitField id="limit-kib" label="Size cap (KiB)" value={v.kib} onChange={set('kib')} />
        <LimitField id="limit-timeout" label="Statement timeout (ms)" value={v.timeout} onChange={set('timeout')} />
        <LimitField id="limit-sample" label="Scan sample (rows)" value={v.sample} onChange={set('sample')} />
      </div>
      <div>
        <Button
          disabled={save.busy || !valid || !changed}
          onClick={() =>
            void save
              .run(() =>
                updateConnection(detail.id, {
                  limits: {
                    max_rows_ceiling: parsed.rows!,
                    max_bytes: parsed.kib! * 1024,
                    statement_timeout: parsed.timeout! * 1e6,
                    scan_sample: parsed.sample!,
                  },
                }),
              )
              .then((ok) => ok && onChanged())
          }
        >
          Save
        </Button>
      </div>
    </SettingSection>
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
 */
export function DetectionEditor({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  const save = useSave()
  const [entities, setEntities] = useState<Record<string, string>>(() =>
    Object.fromEntries((detail.detection ?? []).map((st) => [st.kind, (st.entities ?? []).join(', ')])),
  )
  const stages = detail.detection ?? []
  const on = (k: StageKind) => stages.some((st) => st.kind === k)

  const stageFor = (k: StageKind): Stage => {
    const list = (entities[k] ?? '')
      .split(',')
      .map((e) => e.trim())
      .filter((e) => e !== '')
    const prev = stages.find((st) => st.kind === k)
    return { kind: k, ...(list.length > 0 ? { entities: list } : {}), ...(prev?.raw ? { raw: true } : {}) }
  }
  const apply = (next: Stage[]) =>
    void save.run(() => updateConnection(detail.id, { detection: next })).then((ok) => ok && onChanged())

  return (
    <>
      <SettingSection
        title="Detection"
        save={save}
        why="Each stage gets the text with earlier stages' hits masked, and a text earlier hits cover entirely is not sent on. Detection never sets a column's policy: the catalog does."
      >
        <FieldGroup>
          {KINDS.map((k) => (
            <Field key={k.value}>
              <Field orientation="horizontal">
                <Switch
                  id={`stage-${k.value}`}
                  checked={on(k.value)}
                  disabled={save.busy}
                  onCheckedChange={(v) =>
                    apply(KINDS.map((x) => x.value).filter((x) => (x === k.value ? v : on(x))).map(stageFor))
                  }
                />
                <FieldContent>
                  <FieldLabel htmlFor={`stage-${k.value}`}>{k.title}</FieldLabel>
                  <FieldDescription>{k.finds}</FieldDescription>
                </FieldContent>
              </Field>
              {on(k.value) ? (
                <div className="flex flex-wrap items-end gap-3">
                  <Field className="w-full max-w-sm">
                    <FieldLabel htmlFor={`entities-${k.value}`}>Only these entities (optional)</FieldLabel>
                    <Input
                      id={`entities-${k.value}`}
                      value={entities[k.value] ?? ''}
                      onChange={(e) => setEntities((m) => ({ ...m, [k.value]: e.target.value }))}
                      placeholder="email_address, credit_card"
                    />
                  </Field>
                  <Button
                    variant="outline"
                    disabled={save.busy}
                    onClick={() => apply(KINDS.map((x) => x.value).filter(on).map(stageFor))}
                  >
                    Apply
                  </Button>
                </div>
              ) : null}
            </Field>
          ))}
        </FieldGroup>
        {stages.length === 0 ? (
          <p className="text-sm text-muted-foreground">Off: free text is not examined, so every scan cell is redacted whole.</p>
        ) : null}
      </SettingSection>
      <TermsEditor connId={detail.id} />
    </>
  )
}

/**
 * The list stage's terms and expressions. They are write-only: the daemon
 * answers a save with counts and never sends one back, so the boxes start
 * empty and a save states the whole set. Saving nothing is not how the set is
 * cleared — that is its own action, and it asks first.
 */
function TermsEditor({ connId }: { connId: string }) {
  const save = useSave()
  const [deny, setDeny] = useState('')
  const [allow, setAllow] = useState('')
  const [patterns, setPatterns] = useState('')
  const [stored, setStored] = useState<{ deny: number; allow: number; patterns: number } | null>(null)
  const [clearing, setClearing] = useState(false)

  const lines = (s: string) =>
    s
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l !== '')
  const empty = [deny, allow, patterns].every((t) => t.trim() === '')
  const store = (terms: { deny: string[]; allow: string[]; patterns: Pattern[] }) =>
    void save.run(async () => {
      setStored(await setTerms(connId, terms))
      setDeny('')
      setAllow('')
      setPatterns('')
    })

  return (
    <SettingSection
      title="Your terms"
      save={save}
      why="Terms are as sensitive as the data they describe, so keeper never shows them again once saved. A save replaces the whole set."
    >
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="terms-deny">Deny terms, one per line</FieldLabel>
          <Textarea id="terms-deny" value={deny} onChange={(e) => setDeny(e.target.value)} disabled={save.busy} />
        </Field>
        <Field>
          <FieldLabel htmlFor="terms-patterns">Expressions, one per line as label = expression</FieldLabel>
          <Textarea
            id="terms-patterns"
            value={patterns}
            onChange={(e) => setPatterns(e.target.value)}
            disabled={save.busy}
            placeholder="employee_id = EMP-\d{6}"
          />
          <FieldDescription>
            Go RE2 syntax, so no expression can stall a query. One that does not compile or matches empty text is
            refused by name.
          </FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="terms-allow">Allow terms, one per line</FieldLabel>
          <Textarea id="terms-allow" value={allow} onChange={(e) => setAllow(e.target.value)} disabled={save.busy} />
          <FieldDescription>
            A value any stage finds is left visible only when it equals an allow term exactly, ignoring case.
          </FieldDescription>
        </Field>
      </FieldGroup>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          disabled={save.busy || empty}
          onClick={() => store({ deny: lines(deny), allow: lines(allow), patterns: parsePatterns(patterns) })}
        >
          Save terms
        </Button>
        <Button variant="outline" disabled={save.busy} onClick={() => setClearing(true)}>
          Clear all terms
        </Button>
        {stored ? (
          <span className="text-meta text-muted-foreground">
            stored {stored.deny} deny term(s), {stored.patterns} expression(s), {stored.allow} allow term(s)
          </span>
        ) : null}
      </div>
      <ConfirmWiden
        open={clearing}
        title="Clear all terms?"
        description="The deny terms, expressions and allow terms stored for this connection are deleted. The list stage then finds nothing until you save new ones."
        action="Clear all terms"
        onConfirm={() => {
          setClearing(false)
          store({ deny: [], allow: [], patterns: [] })
        }}
        onCancel={() => setClearing(false)}
      />
    </SettingSection>
  )
}

export function DenylistEditor({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  const [entry, setEntry] = useState('')
  const save = useSave()
  const current = detail.denylist ?? []
  const apply = (next: RelationRef[]) =>
    void save.run(() => setDenylist(detail.id, next)).then((ok) => ok && onChanged())

  return (
    <SettingSection
      title="Denylist"
      save={save}
      why="Evaluated against the plan, so a denied base table is caught even when the statement only names a view over it. No approval overrides it — remove the entry instead, which is a visible edit rather than a click inside a prompt. It is a convenience, not a boundary: the boundary is a grant the role never had."
    >
      {current.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nothing denied.</p>
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
                    disabled={save.busy}
                    onClick={() => apply(current.filter((x) => !(x.schema === r.schema && x.relation === r.relation)))}
                  >
                    Remove
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <div className="flex flex-wrap items-end gap-3">
        <Field className="w-64">
          <FieldLabel htmlFor="deny-entry">schema.relation</FieldLabel>
          <Input id="deny-entry" value={entry} onChange={(e) => setEntry(e.target.value)} />
        </Field>
        <Button
          disabled={save.busy || !entry.includes('.')}
          onClick={() => {
            const [schema, ...rest] = entry.split('.')
            apply([...current, { schema, relation: rest.join('.') }])
            setEntry('')
          }}
        >
          Deny
        </Button>
      </div>
    </SettingSection>
  )
}
