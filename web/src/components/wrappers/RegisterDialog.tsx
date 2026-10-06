import { useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Switch } from '@/components/ui/switch'
import { registerConnection } from '@/lib/api'
import type { Host } from '@/lib/types'

const EMPTY = {
  name: '',
  database: '',
  user: '',
  password: '',
  writes: false,
  catalogPath: '',
}

/**
 * Registering a database on a host, as a modal.
 *
 * The host is already chosen — this dialog opens from its section on
 * `ConnectionsPage` — so it asks only for what differs per connection:
 * database, role and password. Each is a separate field rather than one
 * connection string, because a pasted DSN is where a typo is invisible and the
 * failure arrives later as a permission error nobody can attribute. The
 * daemon's vault assembles the string; it is never built or shown here.
 *
 * There is no engine control: registration records `postgres` and nothing
 * else (internal/daemon/connections.go), and a dropdown with one reachable
 * option is a promise the daemon has not made. For the same reason there is
 * no SSH tunnel section — keeper has none. One profile is one credential;
 * the writes switch says whether its sessions may write at all, and every
 * write still waits for a human.
 *
 * There is no separate "Test connection" button because storing the
 * connection *is* the test: keeper opens the credential to audit the role, and
 * what the audit found is read on `Audit` rather than in here — it is a report
 * about the database, not a step in this form, and the connection works
 * either way.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "dialog"`
 * returns `@shadcn/dialog`, used here, and there is no registry item that
 * composes a credential form.
 */
export function RegisterDialog({
  host,
  onRegistered,
  onError,
}: {
  host: Host
  onRegistered: (c: { id: string }) => Promise<void>
  onError: (e: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [f, setF] = useState(EMPTY)
  const [busy, setBusy] = useState(false)

  const set = <K extends keyof typeof EMPTY>(key: K, value: (typeof EMPTY)[K]) =>
    setF((prev) => ({ ...prev, [key]: value }))

  const ready = f.name.trim() !== '' && f.database.trim() !== '' && f.user.trim() !== ''

  const submit = async () => {
    setBusy(true)
    try {
      const c = await registerConnection({
        name: f.name.trim(),
        host_id: host.id,
        database: f.database.trim(),
        credential: { user: f.user.trim(), password: f.password || undefined },
        writes: f.writes ? 'approve' : 'off',
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
      <DialogTrigger render={<Button variant="outline" size="sm" />}>Add a database</DialogTrigger>
      {/* Wider than the registry's `sm:max-w-sm`. This form pairs user with
          password on one row, and at the default width each of those columns
          is narrower than the value it holds. */}
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Add a database on {host.name}</DialogTitle>
          <DialogDescription>
            {host.address}:{host.port}. keeper stores the credential, audits the role and reports what it holds on
            Audit. The connection works either way.
          </DialogDescription>
        </DialogHeader>

        {/* The field list is taller than a short laptop viewport, and a dialog
            whose title and decision scroll off the top and bottom is a dialog
            you cannot act on. */}
        <ScrollArea className="max-h-dialog-body pr-4">
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="register-name">Name</FieldLabel>
            <Input id="register-name" value={f.name} onChange={(e) => set('name', e.target.value)} />
            <FieldDescription>What an agent names in a query and what the activity log records.</FieldDescription>
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

          <Separator />

          <Field orientation="horizontal">
            <Switch id="register-writes" checked={f.writes} onCheckedChange={(on) => set('writes', on)} />
            <FieldContent>
              <FieldLabel htmlFor="register-writes">Allow writes</FieldLabel>
              <FieldDescription>
                Off, this profile's sessions are read-only at the server. On, an agent may send writes as this user,
                and each one waits in the Inbox showing what it changes.
              </FieldDescription>
            </FieldContent>
          </Field>

          <Separator />


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
