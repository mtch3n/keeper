import { useCallback, useEffect, useState } from 'react'
import { Link, Navigate, useParams } from 'react-router-dom'

import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { buttonVariants } from '@/components/ui/button'
import { getConnection, type ConnectionDetail } from '@/lib/api'
import { CatalogTab } from '@/pages/connection/CatalogTab'
import { OverviewTab } from '@/pages/connection/OverviewTab'
import { TABS, tabPath } from '@/pages/connection/tabs'
import {
  DenylistEditor,
  DetectionEditor,
  LimitsForm,
  ModeSelector,
  Privileges,
  WriteScope,
} from '@/pages/connection/sections'

/**
 * Everything about one database, on one page (UI.md §2.3). It replaces the
 * Catalog and Policy screens, which were each a view of one connection picked
 * again on every visit; the sidebar now picks the connection and the tabs say
 * which part of it is open. Every tab is a route, so each one can be linked to.
 */
export function ConnectionPage() {
  const { id = '', tab = 'overview' } = useParams()
  const [detail, setDetail] = useState<ConnectionDetail | null>(null)
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      setDetail(await getConnection(id))
      setMissing(false)
      setError(null)
    } catch {
      setMissing(true)
    }
  }, [id])

  useEffect(() => {
    void reload()
  }, [reload])

  if (!TABS.some((t) => t.tab === tab)) return <Navigate to={tabPath(id, 'overview')} replace />
  if (missing) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>No such connection</EmptyTitle>
          <EmptyDescription>Nothing is registered under that id. It may have been removed.</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Link to="/connections" className={buttonVariants({ variant: 'outline' })}>
            Back to Connections
          </Link>
        </EmptyContent>
      </Empty>
    )
  }
  if (detail === null || detail.id !== id) return <Skeleton className="h-40 w-full" />

  const changed = () => void reload()
  const tabClass =
    'relative px-3 py-2 text-sm text-muted-foreground transition-colors duration-150 hover:text-foreground ' +
    'after:absolute after:inset-x-3 after:-bottom-px after:h-0.5 after:scale-x-0 after:bg-foreground after:transition-transform after:duration-200 after:ease-settle ' +
    'aria-[current=page]:text-foreground aria-[current=page]:after:scale-x-100'

  return (
    <div className="flex flex-col gap-6">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}
      <div className="flex flex-col gap-1">
        <h1 className="text-title">{detail.name}</h1>
        <span className="text-meta text-muted-foreground">
          {detail.role} on {detail.database}
        </span>
      </div>

      <nav aria-label="Connection sections" className="-mb-6 flex overflow-x-auto border-b border-border">
        {TABS.map((t) => (
          <Link
            key={t.tab}
            to={tabPath(id, t.tab)}
            aria-current={tab === t.tab ? 'page' : undefined}
            className={tabClass}
          >
            {t.label}
          </Link>
        ))}
      </nav>

      <div className="flex flex-col gap-8 pt-6">
        {tab === 'overview' ? <OverviewTab detail={detail} onError={setError} /> : null}
        {tab === 'detection' ? <DetectionEditor key={id} detail={detail} onChanged={changed} onError={setError} /> : null}
        {tab === 'catalog' ? <CatalogTab key={id} connId={id} /> : null}
        {tab === 'limits' ? (
          <>
            <ModeSelector detail={detail} onChanged={changed} onError={setError} />
            <Separator />
            <LimitsForm detail={detail} onChanged={changed} onError={setError} />
            <Separator />
            <DenylistEditor detail={detail} onChanged={changed} onError={setError} />
            <Separator />
            <WriteScope detail={detail} />
          </>
        ) : null}
        {tab === 'privileges' ? <Privileges detail={detail} onChanged={reload} onError={setError} /> : null}
      </div>
    </div>
  )
}
