import { useEffect, type ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { Pagination, PaginationContent, PaginationItem } from '@/components/ui/pagination'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody } from '@/components/ui/table'
import { pageCount, PAGE_SIZES, rangeText, type PageSize, type Paging } from '@/lib/paging'

/**
 * The one table every list uses: the rows of the current page, and under them
 * the count, the range shown, the page size and previous and next.
 *
 * Without `total` the list is whole and this slices it; with `total` the rows
 * are already the current page and the server counted. A list that fits on one
 * page keeps its count and loses the page controls. The table scrolls inside
 * its own region (the `Table` primitive's container), never the page.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "pagination"`
 * returned `@shadcn/pagination`, used here for the control's frame. It has no
 * notion of a page size, a range or a clamped page, which is what this owns.
 */
export function PagedTable<T>({
  paging,
  items,
  total,
  unit = 'rows',
  head,
  row,
  label,
}: {
  paging: Paging
  items: T[]
  total?: number
  unit?: string
  head: ReactNode
  row: (item: T) => ReactNode
  label?: string
}) {
  const { page: asked, size, setPage, clamp, setSize } = paging
  const n = total ?? items.length
  const pages = pageCount(n, size)
  const page = Math.min(asked, pages)
  const visible = total === undefined ? items.slice((page - 1) * size, page * size) : items

  useEffect(() => {
    if (asked > pages && (total === undefined || total > 0)) clamp(pages)
  }, [asked, pages, total, clamp])

  return (
    <div className="flex flex-col gap-3">
      <Table aria-label={label}>
        {head}
        <TableBody>{visible.map(row)}</TableBody>
      </Table>
      <div className="flex flex-wrap items-center justify-between gap-3 text-meta text-muted-foreground">
        <span aria-live="polite">
          {rangeText(page, size, n)} {unit}
        </span>
        {n > PAGE_SIZES[0] ? (
          <label className="flex items-center gap-2">
            Rows per page
            <Select value={String(size)} onValueChange={(v) => setSize(Number(v) as PageSize)}>
              <SelectTrigger size="sm" aria-label="Rows per page" className="w-20">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PAGE_SIZES.map((s) => (
                  <SelectItem key={s} value={String(s)}>
                    {s}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
        ) : null}
        {pages > 1 ? (
          <Pagination className="mx-0 w-auto">
            <PaginationContent>
              <PaginationItem>
                <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>
                  Previous
                </Button>
              </PaginationItem>
              <PaginationItem>
                <span className="px-2">
                  Page {page} of {pages}
                </span>
              </PaginationItem>
              <PaginationItem>
                <Button variant="outline" size="sm" disabled={page >= pages} onClick={() => setPage(page + 1)}>
                  Next
                </Button>
              </PaginationItem>
            </PaginationContent>
          </Pagination>
        ) : null}
      </div>
    </div>
  )
}
