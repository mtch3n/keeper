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
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { registerConnection } from '@/lib/api'
import type { Host, Writes } from '@/lib/types'

const WRITES: { value: Writes; title: string; consequence: string }[] = [
  { value: 'off', title: 'Off', consequence: 'Refuse every write.' },
  { value: 'ask', title: 'Ask', consequence: 'Each write waits in the Inbox, showing what it changes.' },
  { value: 'allow', title: 'Allow', consequence: 'A write commits with no question.' },
]

const EMPTY = {
  name: '',
  database: '',
  user: '',
  password: '',
  writes: 'off' as Writes,
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
 * else, and a dropdown with one reachable option is a promise the daemon has
 * not made. For the same reason there is no SSH tunnel section — keeper has
 * none. One profile is one credential; the writes choice says whether its
 * sessions may write, and whether a write waits for a human.
 *
 * There is no "Test connection" button here because registering does not
 * connect: the test is on the connection's Overview, once it is stored.
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
        writes: f.writes,
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
      <DialogContent className="flex max-h-dialog flex-col overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Add a database on {host.name}</DialogTitle>
          <DialogDescription>
            {host.address}:{host.port}. keeper stores the credential and connects only when something needs it. Test
            it from the connection's Overview.
          </DialogDescription>
        </DialogHeader>

        {/* The field list is taller than a short laptop viewport, and a dialog
            whose title and decision scroll off the top and bottom is a dialog
            you cannot act on. */}
        <div className="min-h-0 flex-1 overflow-y-auto pr-1">
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

          <Field>
            <FieldLabel>Writes</FieldLabel>
            <RadioGroup value={f.writes} onValueChange={(v) => set('writes', v as Writes)} className="flex flex-col gap-3">
              {WRITES.map((o) => (
                <label key={o.value} className="flex gap-4">
                  <RadioGroupItem value={o.value} className="mt-1" />
                  <span className="flex flex-col">
                    <span className="text-sm">{o.title}</span>
                    <span className="text-sm text-muted-foreground">{o.consequence}</span>
                  </span>
                </label>
              ))}
            </RadioGroup>
          </Field>

          <Separator />


          <Field>
            <FieldLabel htmlFor="register-catalog">Catalog path (optional)</FieldLabel>
            <Input
              id="register-catalog"
              value={f.catalogPath}
              onChange={(e) => set('catalogPath', e.target.value)}
            />
            <FieldDescription>Leave empty for keeper's own file for this profile. Set a path to share one catalog file with a project.</FieldDescription>
          </Field>
        </FieldGroup>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !ready}>
            Add database
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
