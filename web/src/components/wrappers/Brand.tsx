import { Link } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { KeeperMark } from '@/components/wrappers/KeeperMark'

/**
 * The mark and the name, linking home.
 *
 * It exists as a component because the brand appears in two places that are
 * never both on screen: `AppSidebar`'s header at `md` and above, where the rail
 * is a column of its own, and `AppShell`'s bar below `md`, where the rail has
 * become an off-canvas `Sheet` and taken the header with it. That is one thing
 * shown in two layouts, which UI.md §3.3 says must be one component — written
 * twice it is two, and the second copy is where the two drift apart.
 */
export function Brand({ className }: { className?: string }) {
  return (
    <Link
      to="/connections"
      className={cn('flex items-center gap-2 text-sm font-semibold text-foreground', className)}
    >
      <KeeperMark />
      keeper
    </Link>
  )
}
