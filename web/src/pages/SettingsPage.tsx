import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { PagedTable } from '@/components/wrappers/PagedTable'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { Lamp } from '@/components/wrappers/Lamp'
import { getDoctor, getSettings, updateSettings, type DoctorReport, type Settings } from '@/lib/api'
import { usePaging } from '@/lib/paging'
import { age } from '@/lib/render'
import { positiveInt, useSave } from '@/lib/save'
import { ConfirmWiden, SettingSection } from '@/components/wrappers/SettingSection'

/**
 * What this daemon is doing.
 *
 * A connection's page answers *what may this connection do*; this screen answers *what is
 * this daemon doing*, which is the question you ask when something is wrong. So
 * it reports rather than configures, and it carries `doctor`'s output rather
 * than a preferences form.
 */

/**
 * The daemon-wide choices: how long the activity log keeps a record, how long a
 * database's connections stay open unused.
 */
function Preferences({ onSaved }: { onSaved: () => void }) {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [confirmOff, setConfirmOff] = useState(false)
  const save = useSave()

  useEffect(() => {
    getSettings()
      .then(setSettings)
      .catch(() => undefined)
  }, [])

  // Each Save sends only its own setting, so one field can never reset another.
  const store = (patch: Partial<Settings>) =>
    void save.run(async () => {
      setSettings(await updateSettings(patch))
      onSaved()
    })

  return (
    <SettingSection title="Preferences" save={save}>
      <FieldGroup>
        <Field>
          <div className="flex items-center gap-3">
            <Switch
              id="ui"
              checked={!settings?.ui_off}
              disabled={settings === null || save.busy}
              onCheckedChange={(on) => (on ? store({ ui_off: false }) : setConfirmOff(true))}
            />
            <FieldLabel htmlFor="ui">Local dashboard</FieldLabel>
          </div>
          <FieldDescription>
            This page and the local links keeper gives for approvals and for typing a value. Off, keeper opens no port;
            approve in the terminal with keeper approve.
          </FieldDescription>
        </Field>
        <ConfirmWiden
          open={confirmOff}
          title="Turn the dashboard off?"
          description="This page stops working at once. Turn it back on with keeper settings set ui on."
          action="Turn off"
          onCancel={() => setConfirmOff(false)}
          onConfirm={() => {
            setConfirmOff(false)
            store({ ui_off: true })
          }}
        />
        <NumberSetting
          id="retention"
          label="Keep the activity log for (days)"
          help="Records older than this are deleted every day. The log is encrypted with the vault's key."
          stored={settings?.log_retention_days}
          busy={save.busy}
          onSave={(n) => store({ log_retention_days: n })}
        />
        <NumberSetting
          id="idle"
          label="Close idle database connections after (minutes)"
          help="keeper connects to a database only when something needs it, and closes the connection once nobody has used it for this long. The next use reconnects."
          stored={settings?.connection_idle_minutes}
          busy={save.busy}
          onSave={(n) => store({ connection_idle_minutes: n })}
        />
      </FieldGroup>
    </SettingSection>
  )
}

/**
 * One whole-number setting: validated as it is typed, and saved only when it
 * changed from what is stored.
 */
function NumberSetting({
  id,
  label,
  help,
  stored,
  busy,
  onSave,
}: {
  id: string
  label: string
  help: string
  stored?: number
  busy: boolean
  onSave: (n: number) => void
}) {
  const [value, setValue] = useState('')
  useEffect(() => {
    if (stored !== undefined) setValue(String(stored))
  }, [stored])
  const parsed = positiveInt(value)
  const invalid = value !== '' && parsed === null

  return (
    <Field data-invalid={invalid || undefined}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="flex items-center gap-3">
        <Input
          id={id}
          inputMode="numeric"
          value={value}
          aria-invalid={invalid}
          onChange={(e) => setValue(e.target.value)}
          className="w-32"
        />
        <Button disabled={busy || parsed === null || parsed === stored} onClick={() => parsed !== null && onSave(parsed)}>
          Save
        </Button>
      </div>
      {invalid ? (
        <FieldDescription className="text-blocked">Enter a whole number above 0.</FieldDescription>
      ) : (
        <FieldDescription>{help}</FieldDescription>
      )}
    </Field>
  )
}

export function SettingsPage() {
  const [report, setReport] = useState<DoctorReport | null>(null)
  const [error, setError] = useState<string | null>(null)
  const sessionPaging = usePaging('sessions')

  const refresh = useCallback(async () => {
    try {
      setReport(await getDoctor())
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-title">Settings</h1>
      {error ? <p className="text-sm text-blocked">{error}</p> : null}
      <Preferences onSaved={() => void refresh()} />
      <Separator />
      {report === null ? (
        <div className="flex flex-col gap-2">
          <p className="text-meta text-muted-foreground">Checking the daemon…</p>
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
      <>

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Daemon</h2>
        <Facts>
          <Fact label="version">{report.version}</Fact>
          <Fact label="sessions">{report.sessions?.length ?? 0} connected</Fact>
          <Fact label="waiting">
            {report.pending_approvals} approval(s), {report.open_requests} local request(s)
          </Fact>
          <Fact label="tickets">{report.open_tickets} open</Fact>
        </Facts>
      </section>

      <Separator />

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Vault</h2>
        {/* There is no locked state and no unlock control. keeperd opens the
            vault before it serves and exits if it cannot, so a daemon this
            screen can reach has an open vault. What is left to report is which
            key source answered. */}
        <Facts>
          <Fact label="state">
            <span className="flex items-center gap-2">
              <Lamp state="live" label="vault open" /> open
            </span>
          </Fact>
          <Fact label="activity log">
            encrypted with the vault's key · kept {report.log_retention_days} day(s)
          </Fact>
          <Fact label="key source">{report.key_source}</Fact>
        </Facts>
      </section>

      <Separator />

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Detection</h2>
        <Facts>
          {report.detector ? (
            <>
              <Fact label="detector">
                {report.detector.name} {report.detector.version ?? ''}
              </Fact>
              {/* "What was examining my data, and could it talk to anyone" has to
                  be answerable after the fact, so it is answerable now. */}
              <Fact label="network">
                {report.detector.network_posture === 'none' ? (
                  'no network access'
                ) : (
                  <span className="text-waiting">{report.detector.network_posture}</span>
                )}
              </Fact>
            </>
          ) : (
            <Fact label="detector">none reported</Fact>
          )}
        </Facts>
      </section>

      <Separator />

      {report.sessions?.length ? (
        <>
          <Separator />
          <section className="flex flex-col gap-3">
            <h2 className="text-heading">Sessions</h2>
            <PagedTable
              paging={sessionPaging}
              items={[...report.sessions].sort((a, b) => a.connected_at.localeCompare(b.connected_at) || a.id.localeCompare(b.id))}
              unit="sessions"
              label="Sessions"
              head={
                <TableHeader>
                  <TableRow>
                    <TableHead>Client</TableHead>
                    <TableHead>Workspace</TableHead>
                    <TableHead className="text-right">Connected</TableHead>
                  </TableRow>
                </TableHeader>
              }
              row={(s) => (
                <TableRow key={s.id}>
                  <TableCell className="text-meta">
                    {s.client.name} {s.client.version ?? ''}
                  </TableCell>
                  <TableCell className="text-meta">{s.client.workspace ?? '—'}</TableCell>
                  <TableCell className="text-right text-meta">{age(s.connected_at)} ago</TableCell>
                </TableRow>
              )}
            />
            <p className="text-meta text-muted-foreground">Closing a session discards its tickets and tokens.</p>
          </section>
        </>
      ) : null}
      </>
      )}
    </div>
  )
}
