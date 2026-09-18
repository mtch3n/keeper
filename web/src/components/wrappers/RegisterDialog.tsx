import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Separator } from '@/components/ui/separator'
import { ScrollArea } from '@/components/ui/scroll-area'
import { registerConnection } from '@/lib/api'

/** Assembles the connection string the daemon is given. It is built here and
 * never shown back: the password is in it, and a field that redisplays a
 * credential is a credential on a screen. */
function toDsn(f: { host: string; port: string; database: string; user: string; password: string; tls: boolean }) {
  const auth = f.password ? `${encodeURIComponent(f.user)}:${encodeURIComponent(f.password)}` : encodeURIComponent(f.user)
  const port = f.port.trim() === '' ? '5432' : f.port.trim()
  const sslmode = f.tls ? 'require' : 'prefer'
  return `postgres://${auth}@${f.host.trim()}:${port}/${encodeURIComponent(f.database.trim())}?sslmode=${sslmode}`
}

const EMPTY = {
  name: '',
  host: 'localhost',
  port: '5432',
  database: '',
  user: '',
  password: '',
  tls: false,
  writeUser: '',
  writePassword: '',
  catalogPath: '',
  raw: '',
}

/**
 * Registering a database, as a modal.
 *
 * It takes host, port, user, password and database separately rather than one
 * connection string, because a pasted DSN is the one field where a typo is
 * invisible and the failure arrives later as a permission error nobody can
 * attribute. The string is assembled here and never rendered back.
 *
 * There is no engine control: registration records `postgres` and nothing
 * else (internal/daemon/connections.go), and a dropdown with one reachable
 * option is a promise the daemon has not made. For the same reason there is
 * no SSH tunnel section and no read-only switch — keeper has no tunnel, and
 * connection mode is `Policy`'s `RadioGroup` with a sentence of consequence
 * under each option, which a checkbox here would quietly become a third
 * rendering of.
 *
 * The audit is the test. There is no separate "Test connection" button
 * because storing the connection *is* the test: keeper audits the role, keeps
 * the connection disabled, and enables it only once each finding has been
 * accepted. The findings themselves are read on the page rather than in here,
 * where a checkbox per finding and its narrower grant would be cramped.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "dialog"`
 * returns `@shadcn/dialog`, used here, and there is no registry item that
 * composes a credential form with the DSN assembly above. This wrapper owns
 * that assembly and the two entry modes.
 */
export function RegisterDialog({
  onRegistered,
  onError,
}: {
  onRegistered: (c: { id: string }) => Promise<void>
  onError: (e: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [pasted, setPasted] = useState(false)
  const [f, setF] = useState(EMPTY)
  const [busy, setBusy] = useState(false)

  const set = <K extends keyof typeof EMPTY>(key: K, value: (typeof EMPTY)[K]) =>
    setF((prev) => ({ ...prev, [key]: value }))

  const ready =
    f.name.trim() !== '' &&
    (pasted ? f.raw.trim() !== '' : f.host.trim() !== '' && f.database.trim() !== '' && f.user.trim() !== '')

  const submit = async () => {
    setBusy(true)
    try {
      const dsn = pasted ? f.raw.trim() : toDsn(f)
      const writeDsn =
        !pasted && f.writeUser.trim() !== ''
          ? toDsn({ ...f, user: f.writeUser, password: f.writePassword })
          : undefined
      const c = await registerConnection({
        name: f.name.trim(),
        dsn,
        write_dsn: writeDsn,
        catalog_path: f.catalogPath.trim() || undefined,
      })
      setF(EMPTY)
      setOpen(false)
      await onRegistered(c)
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant="outline" />}>Register a database</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Register a database</DialogTitle>
          <DialogDescription>
            keeper audits the role and reports what it holds. The connection is stored disabled and becomes usable
            once you have accepted each finding.
          </DialogDescription>
        </DialogHeader>

        {/* The field list is taller than a short laptop viewport once the
            write credential is open, and a dialog whose title and decision
            scroll off the top and bottom is a dialog you cannot act on. */}
        <ScrollArea className="max-h-dialog-body pr-4">
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="register-name">Name</FieldLabel>
            <Input id="register-name" value={f.name} onChange={(e) => set('name', e.target.value)} />
            <FieldDescription>What an agent names in a query and what the activity log records.</FieldDescription>
          </Field>

          <Field orientation="horizontal">
            <FieldLabel htmlFor="register-paste">Paste a connection string instead</FieldLabel>
            <Switch id="register-paste" checked={pasted} onCheckedChange={setPasted} />
          </Field>

          {pasted ? (
            <Field>
              <FieldLabel htmlFor="register-raw">Connection string</FieldLabel>
              <Input
                id="register-raw"
                type="password"
                value={f.raw}
                onChange={(e) => set('raw', e.target.value)}
                placeholder="postgres://user:password@host:5432/database"
              />
              <FieldDescription>Stored in the vault and never shown again.</FieldDescription>
            </Field>
          ) : (
            <>
              <Field orientation="horizontal">
                <Field>
                  <FieldLabel htmlFor="register-host">Host</FieldLabel>
                  <Input id="register-host" value={f.host} onChange={(e) => set('host', e.target.value)} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="register-port">Port</FieldLabel>
                  <Input id="register-port" value={f.port} onChange={(e) => set('port', e.target.value)} />
                </Field>
              </Field>

              <Field>
                <FieldLabel htmlFor="register-database">Database</FieldLabel>
                <Input id="register-database" value={f.database} onChange={(e) => set('database', e.target.value)} />
              </Field>

              <Field orientation="horizontal">
                <Field>
                  <FieldLabel htmlFor="register-user">User</FieldLabel>
                  <Input id="register-user" value={f.user} onChange={(e) => set('user', e.target.value)} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="register-password">Password</FieldLabel>
                  <Input
                    id="register-password"
                    type="password"
                    value={f.password}
                    onChange={(e) => set('password', e.target.value)}
                  />
                </Field>
              </Field>

              <Field orientation="horizontal">
                <FieldLabel htmlFor="register-tls">Require TLS</FieldLabel>
                <Switch id="register-tls" checked={f.tls} onCheckedChange={(v) => set('tls', v)} />
              </Field>

              <Separator />

              <Collapsible>
                <CollapsibleTrigger render={<Button variant="ghost" size="sm" />}>
                  Write credential (optional)
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <FieldGroup>
                    <FieldDescription>
                      A second role on the same host and database. Without one, write mode does not exist for this
                      connection and no setting here creates it.
                    </FieldDescription>
                    <Field orientation="horizontal">
                      <Field>
                        <FieldLabel htmlFor="register-write-user">Write user</FieldLabel>
                        <Input
                          id="register-write-user"
                          value={f.writeUser}
                          onChange={(e) => set('writeUser', e.target.value)}
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor="register-write-password">Write password</FieldLabel>
                        <Input
                          id="register-write-password"
                          type="password"
                          value={f.writePassword}
                          onChange={(e) => set('writePassword', e.target.value)}
                        />
                      </Field>
                    </Field>
                  </FieldGroup>
                </CollapsibleContent>
              </Collapsible>
            </>
          )}

          <Field>
            <FieldLabel htmlFor="register-catalog">Catalog path</FieldLabel>
            <Input
              id="register-catalog"
              value={f.catalogPath}
              onChange={(e) => set('catalogPath', e.target.value)}
              placeholder=".keeper/catalog.yaml"
            />
            <FieldDescription>Lives in the project repository and is reviewed like code.</FieldDescription>
          </Field>
        </FieldGroup>
        </ScrollArea>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !ready}>
            Audit and store
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
