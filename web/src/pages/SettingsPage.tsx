import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { Lamp } from '@/components/wrappers/Lamp'
import { getDoctor, getSettings, updateSettings, type DoctorReport, type Settings } from '@/lib/api'
import { notificationsOn, setNotificationsOn } from '@/lib/notify'
import { PermissionsSection } from '@/pages/PermissionsSection'
import { age } from '@/lib/render'
import { positiveInt, useSave } from '@/lib/save'
import { SettingSection } from '@/components/wrappers/SettingSection'

/**
 * What this daemon is doing (UI.md §2.3, §2.7).
 *
 * A connection's page answers *what may this connection do*; this screen answers *what is
 * this daemon doing*, which is the question you ask when something is wrong. So
 * it reports rather than configures, and it carries `doctor`'s output rather
 * than a preferences form.
 */

/**
 * The daemon-wide choices: how long the activity log keeps a record, how long a
 * database's connections stay open unused, and whether this browser raises a
 * notification when something new waits in the Inbox.
 */
function Preferences() {
  const [settings, setSettings] = useState<Settings | null>(null)
  const save = useSave()
  const [notify, setNotify] = useState(notificationsOn())
  const [permission, setPermission] = useState(typeof Notification === 'undefined' ? 'unsupported' : Notification.permission)

  useEffect(() => {
    getSettings()
      .then(setSettings)
      .catch(() => undefined)
  }, [])

  // Each Save sends only its own setting, so one field can never reset another.
  const store = (patch: Partial<Settings>) =>
    void save.run(async () => setSettings(await updateSettings(patch)))

  return (
    <SettingSection title="Preferences" save={save}>
      <FieldGroup>
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
        <Field orientation="horizontal">
          <Switch
            id="notify"
            checked={notify}
            onCheckedChange={(on) => {
              setNotificationsOn(on)
              setNotify(on)
              if (on && typeof Notification !== 'undefined' && Notification.permission === 'default') {
                void Notification.requestPermission().then(setPermission)
              }
            }}
          />
          <FieldContent>
            <FieldLabel htmlFor="notify">Notify me when something waits in the Inbox</FieldLabel>
            <FieldDescription>
              {permission === 'denied'
                ? 'This browser has blocked notifications for keeper; allow them in its site settings.'
                : permission === 'unsupported'
                  ? 'This browser cannot show notifications.'
                  : 'Only while keeper is open in a tab you are not looking at.'}
            </FieldDescription>
          </FieldContent>
        </Field>
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
      <Preferences />
      <Separator />
      <PermissionsSection />
      <Separator />
      {report === null ? (
        <div className="flex flex-col gap-2">
          <p className="text-meta text-muted-foreground">Checking the daemon and every connection…</p>
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
          <Fact label="allow rules">
            {report.grants} · {report.suspended_grants} suspended by a schema change
          </Fact>
        </Facts>
      </section>

      <Separator />

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Vault</h2>
        {/* There is no locked state and no unlock control. keeperd opens the
            vault from the keychain, KEEPER_MASTER_KEY or key.age before it
            serves and exits if it cannot (§4.3), so a daemon this screen can
            reach has an open vault. What is left to report is which source
            answered: silent degradation to a weaker one is a defect (R4.3), so
            it is named rather than assumed. */}
        <Facts>
          <Fact label="state">
            <span className="flex items-center gap-2">
              <Lamp state="live" label="vault open" /> open
            </span>
          </Fact>
          <Fact label="activity log">
            encrypted with the vault's key · kept {report.log_retention_days} day(s)
            {report.legacy_audit_log ? (
              <div className="text-meta text-waiting">
                A plaintext log from an earlier keeper is still at {report.legacy_audit_log}. It is never read; delete it
                when you no longer need it.
              </div>
            ) : null}
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
                  be answerable after the fact (R8.5g), so it is answerable now. */}
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

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Connections</h2>
        {report.connections?.length ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Mode</TableHead>
                <TableHead className="text-right">Findings</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {report.connections.map((c) => (
                <TableRow key={c.id}>
                  <TableCell className="text-meta">
                    <Link to={`/connections/${c.id}`} className="hover:underline">
                      {c.name}
                    </Link>
                  </TableCell>
                  <TableCell className="text-meta">{c.mode}</TableCell>
                  {/* A count and a link, never a state: a finding does not stop
                      this connection, and doctor calling it a fault would be the
                      acceptance gate under another name (SPEC R4.1). */}
                  <TableCell className="text-right text-meta">
                    {c.findings > 0 ? (
                      <Link to={`/connections/${c.id}/privileges`} className="underline underline-offset-4">
                        {c.findings}
                      </Link>
                    ) : (
                      '—'
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-meta text-muted-foreground">no connections registered</p>
        )}
      </section>

      {report.sessions?.length ? (
        <>
          <Separator />
          <section className="flex flex-col gap-3">
            <h2 className="text-heading">Sessions</h2>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Client</TableHead>
                  <TableHead>Workspace</TableHead>
                  <TableHead>Intent</TableHead>
                  <TableHead className="text-right">Connected</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {report.sessions.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell className="text-meta">
                      {s.client.name} {s.client.version ?? ''}
                    </TableCell>
                    <TableCell className="text-meta">{s.client.workspace ?? '—'}</TableCell>
                    <TableCell className="text-sm">{s.intent ?? '—'}</TableCell>
                    <TableCell className="text-right text-meta">{age(s.connected_at)} ago</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <p className="text-meta text-muted-foreground">
              A session is one connection to the daemon. When it closes, its tickets, its tokens and its
              session-scoped grants go with it.
            </p>
          </section>
        </>
      ) : null}
      </>
      )}
    </div>
  )
}
