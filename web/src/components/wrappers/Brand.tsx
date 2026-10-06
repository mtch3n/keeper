import { Link } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { KeeperMark } from '@/components/wrappers/KeeperMark'

/** The mark and the name, linking home, at the start of the top bar. */
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
