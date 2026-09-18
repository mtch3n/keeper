import type { ComponentProps, ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

/**
 * An icon-only button that says what it does. The label is both the
 * accessible name and a tooltip, so an icon never has to be guessed at, and a
 * `title` attribute (slow, unstyled, absent on touch) is never the answer.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "icon button"`
 * returned no items and `-q "button"` returns only `@shadcn/button`, which
 * has no notion of an accessible label paired with a tooltip. Composition of
 * Button + Tooltip cannot be inlined at every call site without repeating the
 * pairing, so this owns exactly that pairing and nothing else.
 */
export function IconButton({
  label,
  children,
  side = 'bottom',
  variant = 'ghost',
  size = 'icon-sm',
  ...props
}: Omit<ComponentProps<typeof Button>, 'aria-label' | 'title'> & {
  label: string
  children: ReactNode
  side?: 'top' | 'bottom' | 'left' | 'right'
}) {
  return (
    <Tooltip>
      <TooltipTrigger render={<Button variant={variant} size={size} aria-label={label} {...props} />}>
        {children}
      </TooltipTrigger>
      <TooltipContent side={side}>{label}</TooltipContent>
    </Tooltip>
  )
}
