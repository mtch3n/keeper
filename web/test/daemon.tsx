import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { act } from '@testing-library/react'
import { vi } from 'vitest'

import { streams } from './setup'

import App from '@/App'
import { TooltipProvider } from '@/components/ui/tooltip'
import { LiveStatusProvider } from '@/components/wrappers/LiveStatus'
import type { Finding } from '@/lib/types'

/** A route answers with [status, body]; `null` body is an empty 204. */
export type Handler = (body: unknown) => [number, unknown]

export interface FakeDaemon {
  calls: string[]
  on(method: string, path: string, handler: Handler): void
}

/** Stands in for keeperd at the fetch boundary, answering by method and path.
 * Anything unrouted answers an empty list, so a page's unrelated loads stay quiet. */
export function fakeDaemon(): FakeDaemon {
  const routes = new Map<string, Handler>()
  const calls: string[] = []
  vi.stubGlobal('fetch', async (input: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const path = input.split('?')[0]
    calls.push(`${method} ${input}`)
    const handler = routes.get(`${method} ${path}`)
    const [status, body] = handler ? handler(init?.body ? JSON.parse(String(init.body)) : undefined) : [200, []]
    return new Response(body === null ? null : JSON.stringify(body), { status: body === null ? 204 : status })
  })
  return { calls, on: (method, path, handler) => routes.set(`${method} ${path}`, handler) }
}

export function renderApp(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
          <LiveStatusProvider>
            <TooltipProvider delay={400}>
              <App />
            </TooltipProvider>
          </LiveStatusProvider>
    </MemoryRouter>,
  )
}

export const NEVER = '0001-01-01T00:00:00Z'

export function summary(id: string, name: string) {
  return {
    id,
    name,
    engine: 'postgres',
    mode: 'assisted',
    host: 'db1',
    address: 'db1.internal',
    port: 5432,
    database: `${name}_db`,
    username: `${name}_ro`,
    writes: 'off',
  }
}

export function detail(id: string, name: string, auditedAt: string, findings: Finding[]) {
  return {
    ...summary(id, name),
    audited_privileges: { audited_at: auditedAt, findings },
    catalog_status: { path: '.keeper/catalog.yaml', fresh: true, freshness_known: true, unclassified: 0 },
    detection: [{ kind: 'patterns' }],
    limits: { max_rows_ceiling: 1000, max_bytes: 1048576, statement_timeout: 30000000000, scan_sample: 300 },
  }
}

export function finding(id: string, detailText: string, narrower: string): Finding {
  return { id, kind: 'relation-write', subject: id, detail: detailText, narrower }
}

/** Pushes one daemon event to every open event stream, as keeperd would. */
export function emit(kind: string, data: unknown) {
  act(() => {
    for (const s of [...streams]) {
      for (const fn of s.listeners.get(kind) ?? []) fn(new MessageEvent(kind, { data: JSON.stringify(data) }))
    }
  })
}

export function session(id: string, name: string, workspace: string, intent: string) {
  return { id, client: { name, version: '1', pid: 1, workspace }, intent, connected_at: '2026-10-06T12:00:00Z' }
}

export function doctorReport(extra: Record<string, unknown> = {}) {
  return {
    version: 'test', key_source: 'test', pending_approvals: 0, open_tickets: 0, open_requests: 0, grants: 0,
    suspended_grants: 0, log_retention_days: 30, connections: [], sessions: [], ...extra,
  }
}
