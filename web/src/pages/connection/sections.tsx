import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { updateConnection, type ConnectionDetail } from '@/lib/api'
import { positiveInt, useSave } from '@/lib/save'
import { ConfirmWiden, SettingSection } from '@/components/wrappers/SettingSection'
import type { Detection, Reads, Writes } from '@/lib/types'

type Option<T extends string> = { value: T; title: string; consequence: string }

const READS: Option<Reads>[] = [
  { value: 'always', title: 'Always', consequence: 'Ask you before every read.' },
  { value: 'uncertain', title: 'Uncertain', consequence: 'Ask only when a read may change data, such as calling a function that writes.' },
  {
    value: 'never',
    title: 'Never',
    consequence: 'Skips the question. The transaction still blocks database writes.',
  },
]

const WRITES: Option<Writes>[] = [
  { value: 'off', title: 'Off', consequence: 'Refuse every write.' },
  { value: 'ask', title: 'Ask', consequence: 'Each write waits for you to accept it.' },
  { value: 'allow', title: 'Allow', consequence: 'Commits with no question.' },
]

const DETECTION: Option<Detection>[] = [
  { value: 'off', title: 'Off', consequence: 'Do not scan text. Scan columns are redacted whole.' },
  { value: 'scan', title: 'Scan columns', consequence: 'Redact personal data found in the text of scan columns.' },
  { value: 'all', title: 'All columns', consequence: 'Also scan the text of allow columns. Token columns stay tokens.' },
]

/** A choice rendered as text with its consequence under it. */
function Choices<T extends string>({
  value,
  options,
  disabled,
  onChoose,
}: {
  value: T
  options: Option<T>[]
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
 * One choice that saves at once. A choice that widens what an agent may do
 * asks first; one that narrows does not.
 */
function ChoiceSetting<T extends string>({
  title,
  why,
  value,
  options,
  field,
  detail,
  onChanged,
  widens,
  confirm,
}: {
  title: string
  why: string
  value: T
  options: Option<T>[]
  field: 'reads' | 'writes' | 'detection'
  detail: ConnectionDetail
  onChanged: () => void
  widens?: (next: T) => boolean
  confirm?: (next: T) => { title: string; description: string; action: string }
}) {
  const save = useSave()
  const [pending, setPending] = useState<T | null>(null)
  const apply = (next: T) =>
    void save.run(() => updateConnection(detail.id, { [field]: next })).then((ok) => ok && onChanged())
  const ask = pending !== null && confirm ? confirm(pending) : null

  return (
    <SettingSection title={title} save={save} why={why}>
      <Choices
        value={value}
        options={options}
        disabled={save.busy}
        onChoose={(next) => (next !== value && widens?.(next) ? setPending(next) : apply(next))}
      />
      <ConfirmWiden
        open={ask !== null}
        title={ask?.title ?? ''}
        description={ask?.description}
        action={ask?.action ?? ''}
        onConfirm={() => {
          const next = pending!
          setPending(null)
          apply(next)
        }}
        onCancel={() => setPending(null)}
      />
    </SettingSection>
  )
}

export function ReadsSetting({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  return (
    <ChoiceSetting
      title="Reads"
      why="Reads never change data under any choice: each runs in a read-only transaction. This only decides whether a read waits for you."
      value={detail.reads}
      options={READS}
      field="reads"
      detail={detail}
      onChanged={onChanged}
      widens={(next) => next === 'never'}
      confirm={() => ({
        title: 'Never ask about reads?',
        description: 'Agents on this profile may then run every read with no question. The database still blocks any write.',
        action: 'Never ask',
      })}
    />
  )
}

export function WritesSetting({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  return (
    <ChoiceSetting
      title="Writes"
      why="keeper never switches profiles. A write sent on a profile with writes off is refused, naming the profiles on this database that allow writes."
      value={detail.writes}
      options={WRITES}
      field="writes"
      detail={detail}
      onChanged={onChanged}
      widens={(next) => next === 'allow' || (next === 'ask' && detail.writes === 'off')}
      confirm={(next) =>
        next === 'allow'
          ? {
              title: `Allow writes as ${detail.username}?`,
              description: 'Agents on this profile may then send INSERT, UPDATE and DELETE, and each commits with no question.',
              action: 'Allow writes',
            }
          : {
              title: `Ask about writes as ${detail.username}?`,
              description: 'Agents on this profile may then send INSERT, UPDATE and DELETE. Each waits for you in the Inbox.',
              action: 'Ask about writes',
            }
      }
    />
  )
}

export function DetectionSetting({ detail, onChanged }: { detail: ConnectionDetail; onChanged: () => void }) {
  return (
    <ChoiceSetting
      title="Detection"
      why="Detection only removes more. It never loosens a column's policy: the catalog does."
      value={detail.detection}
      options={DETECTION}
      field="detection"
      detail={detail}
      onChanged={onChanged}
    />
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
