import * as React from "react"
import { cn } from "@/lib/utils"

/**
 * A surface, and the padding inside it.
 *
 * The registry ships `CardHeader`, `CardTitle`, `CardDescription`,
 * `CardAction` and `CardFooter` alongside these two. None of them was used by
 * a single screen, and each was carrying styling that fought this theme rather
 * than serving it: the footer drew `border-t bg-muted/50`, a second surface
 * tint inside a surface, which is exactly the "panel almost the same colour as
 * the thing under it" problem the palette was just rewritten to remove. They
 * are deleted rather than left unused — an unused slot is a slot the next
 * screen reaches for, and it would have brought that treatment back with it.
 *
 * A header is a heading and a paragraph. Screens here already write those with
 * `text-heading` and `text-sm text-muted-foreground` from the one type scale,
 * which is where the sizes are decided; `CardTitle`'s `text-base` was off that
 * scale outright and only survived `ui-audit` because registry files are
 * exempt from the type-scale check. A footer is a row of buttons, which is a
 * flex row. Neither needed a component, and having one meant the card's own
 * box had to know about them — `has-data-[slot=card-footer]:pb-0` existed only
 * so the footer could reach back and cancel the padding it was sitting in.
 *
 * The radius utilities went with them. `rounded-xl` resolves to 0 through
 * `--radius`, so `rounded-t-xl` / `rounded-b-xl` on the first and last child
 * were three selectors describing a corner that is square (UI.md §3.2).
 *
 * Never for row data: rows of like things are a `Table` (UI.md §3.3).
 */
function Card({
  className,
  size = "default",
  ...props
}: React.ComponentProps<"div"> & { size?: "default" | "sm" }) {
  return (
    <div
      data-slot="card"
      data-size={size}
      className={cn(
        "group/card flex flex-col gap-(--card-spacing) overflow-hidden bg-card py-(--card-spacing) text-sm text-card-foreground ring-1 ring-foreground/10 [--card-spacing:--spacing(4)] data-[size=sm]:[--card-spacing:--spacing(3)]",
        className
      )}
      {...props}
    />
  )
}

function CardContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-content"
      className={cn("px-(--card-spacing)", className)}
      {...props}
    />
  )
}

export { Card, CardContent }
