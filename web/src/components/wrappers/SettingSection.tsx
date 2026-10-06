import { useState, type ReactNode } from 'react'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import type { SaveState } from '@/lib/save'

const WHY_KEY = 'keeper.why.'

/**
 * One setting on a settings page: its heading, its controls, a line saying it
 * saved or why it did not, and — behind "Why" — the reasoning a first-time
 * reader needs and a returning one does not. The sentence of consequence under
 * each option belongs in the controls, not here: it is what a choice is
 * decided on.
 */
export function SettingSection({
  title,
  why,
  save,
  children,
}: {
  title: string
  why?: ReactNode
  save?: SaveState
  children: ReactNode
}) {
  const id = `setting-${title.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`
  const [open, setOpen] = useState(() => localStorage.getItem(WHY_KEY + id) === 'open')

  return (
    <section aria-labelledby={id} className="flex flex-col gap-4">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
        <h2 id={id} className="text-heading">
          {title}
        </h2>
        <span aria-live="polite" className="text-meta text-muted-foreground">
          {save?.status === 'saving' ? 'Saving…' : save?.status === 'saved' ? 'Saved' : ''}
        </span>
      </div>
      {children}
      {save?.status === 'failed' ? (
        <p role="alert" className="text-sm text-blocked">
          {save.error}
        </p>
      ) : null}
      {why ? (
        <Collapsible
          open={open}
          onOpenChange={(next) => {
            setOpen(next)
            localStorage.setItem(WHY_KEY + id, next ? 'open' : 'closed')
          }}
        >
          <CollapsibleTrigger className="text-sm text-muted-foreground underline underline-offset-4 hover:text-foreground">
            Why
          </CollapsibleTrigger>
          <CollapsibleContent>
            <p className="mt-2 max-w-prose text-sm text-muted-foreground">{why}</p>
          </CollapsibleContent>
        </Collapsible>
      ) : null}
    </section>
  )
}

/**
 * The one confirmation a setting asks for: before it widens what an agent may
 * do. Narrowing never asks, because undoing a narrowing costs nothing.
 */
export function ConfirmWiden({
  open,
  title,
  description,
  action,
  onConfirm,
  onCancel,
}: {
  open: boolean
  title: string
  description: ReactNode
  action: string
  onConfirm: () => void
  onCancel: () => void
}) {
  return (
    <AlertDialog open={open} onOpenChange={(next) => (next ? undefined : onCancel())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>{action}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
