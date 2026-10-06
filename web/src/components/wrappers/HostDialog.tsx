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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { registerHost } from '@/lib/api'
import { SSL_MODES, type SSLMode } from '@/lib/types'

const EMPTY = { name: '', address: 'localhost', port: '5432', sslmode: 'prefer' as SSLMode }

/**
 * Registering a database server, as a modal. A host is where to connect and
 * nothing about who connects: every database and role on it is registered
 * afterwards with `RegisterDialog`, so an address is typed once however many
 * credentials share it.
 *
 * sslmode is a `Select` of libpq's six values rather than a TLS switch: a
 * managed server that wants `verify-full` and a local one that wants
 * `disable` are both ordinary, and a switch can only say two of six things.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "dialog"` returns
 * `@shadcn/dialog`, used here; nothing in the registry composes it with a
 * server-address form.
 */
export function HostDialog({
  onRegistered,
  onError,
}: {
  onRegistered: () => Promise<void>
  onError: (e: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [f, setF] = useState(EMPTY)
  const [busy, setBusy] = useState(false)

  const set = <K extends keyof typeof EMPTY>(key: K, value: (typeof EMPTY)[K]) =>
    setF((prev) => ({ ...prev, [key]: value }))

  const port = Number(f.port.trim() || '5432')
  const ready = f.name.trim() !== '' && f.address.trim() !== '' && Number.isInteger(port)

  const submit = async () => {
    setBusy(true)
    try {
      await registerHost({ name: f.name.trim(), address: f.address.trim(), port, sslmode: f.sslmode })
      setF(EMPTY)
      setOpen(false)
      await onRegistered()
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant="outline" />}>Add a host</DialogTrigger>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Add a host</DialogTitle>
          <DialogDescription>
            A database server. keeper stores where it is and connects to nothing until you register a database on it.
          </DialogDescription>
        </DialogHeader>

        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="host-name">Name</FieldLabel>
            <Input id="host-name" value={f.name} onChange={(e) => set('name', e.target.value)} />
            <FieldDescription>How this server is labelled here and in the CLI. Never sent to an agent.</FieldDescription>
          </Field>

          <Field orientation="horizontal">
            <Field>
              <FieldLabel htmlFor="host-address">Address</FieldLabel>
              <Input id="host-address" value={f.address} onChange={(e) => set('address', e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="host-port">Port</FieldLabel>
              <Input id="host-port" value={f.port} onChange={(e) => set('port', e.target.value)} />
            </Field>
          </Field>

          <Field>
            <FieldLabel htmlFor="host-sslmode">TLS (sslmode)</FieldLabel>
            <Select value={f.sslmode} onValueChange={(v) => set('sslmode', v as SSLMode)}>
              <SelectTrigger id="host-sslmode" className="w-48">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SSL_MODES.map((m) => (
                  <SelectItem key={m} value={m}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FieldDescription>Managed servers usually need require or stricter.</FieldDescription>
          </Field>
        </FieldGroup>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !ready}>
            Add host
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
