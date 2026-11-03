import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

export const PAGE_SIZES = [25, 50, 100] as const
export type PageSize = (typeof PAGE_SIZES)[number]

export interface Paging {
  id: string
  page: number
  size: PageSize
  setPage: (page: number, replace?: boolean) => void
  /** Lowers the page to `last` if it is past it; never raises it. */
  clamp: (last: number) => void
  setSize: (size: PageSize) => void
}

const sizeKey = (id: string) => `keeper.pagesize.${id}`

function storedSize(id: string): PageSize {
  const n = Number(localStorage.getItem(sizeKey(id)))
  return PAGE_SIZES.find((s) => s === n) ?? PAGE_SIZES[0]
}

/**
 * The page and page size of one list. The page lives in the URL query as
 * `<id>_page`, so a reload and the back button keep it; the size is kept per
 * list in localStorage. A change in `resetKey`, such as a filter, goes back to
 * page 1. `memory` keeps the page in state instead, for a list inside a panel
 * that is not a place of its own.
 */
export function usePaging(id: string, resetKey = '', memory = false): Paging {
  const [params, setParams] = useSearchParams()
  const [local, setLocal] = useState(1)
  const [size, setSizeState] = useState<PageSize>(() => storedSize(id))
  const param = `${id}_page`
  const page = memory ? local : Math.max(1, Math.floor(Number(params.get(param))) || 1)

  const setPage = useCallback(
    (next: number, replace = false) => {
      if (memory) return setLocal(next)
      setParams(
        (prev) => {
          const p = new URLSearchParams(prev)
          if (next <= 1) p.delete(param)
          else p.set(param, String(next))
          return p
        },
        { replace },
      )
    },
    [memory, param, setParams],
  )

  // A router's functional search-params update is not queued behind an earlier
  // one, so a clamp made for the old size must not overwrite the reset to page 1.
  const resetting = useRef(false)
  useEffect(() => {
    if (page === 1) resetting.current = false
  }, [page])

  const clamp = useCallback(
    (last: number) => {
      if (resetting.current) return
      if (memory) return setLocal((p) => Math.min(p, last))
      setParams(
        (prev) => {
          if ((Number(prev.get(param)) || 1) <= last) return prev
          const p = new URLSearchParams(prev)
          if (last <= 1) p.delete(param)
          else p.set(param, String(last))
          return p
        },
        { replace: true },
      )
    },
    [memory, param, setParams],
  )

  const setSize = useCallback(
    (next: PageSize) => {
      localStorage.setItem(sizeKey(id), String(next))
      resetting.current = page !== 1
      setSizeState(next)
      setPage(1)
    },
    [id, page, setPage],
  )

  const last = useRef(resetKey)
  useEffect(() => {
    if (last.current === resetKey) return
    last.current = resetKey
    setPage(1, true)
  }, [resetKey, setPage])

  return { id, page, size, setPage, clamp, setSize }
}

/** The pages a list of `total` rows has at this size; an empty list has one. */
export function pageCount(total: number, size: number): number {
  return Math.max(1, Math.ceil(total / size))
}

/** The 1-based range shown on a page, as `51–100 of 312`. */
export function rangeText(page: number, size: number, total: number): string {
  if (total === 0) return '0 of 0'
  const from = (page - 1) * size + 1
  return `${from}–${Math.min(total, page * size)} of ${total}`
}
