import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'

import App from '@/App'
import { TooltipProvider } from '@/components/ui/tooltip'
import { ConnectionScopeProvider } from '@/components/wrappers/ConnectionScopeProvider'
import { LiveStatusProvider } from '@/components/wrappers/LiveStatus'
import { SessionScopeProvider } from '@/components/wrappers/SessionScopeProvider'
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
      <ConnectionScopeProvider>
        <SessionScopeProvider>
          <LiveStatusProvider>
            <TooltipProvider delay={400}>
              <App />
            </TooltipProvider>
          </LiveStatusProvider>
        </SessionScopeProvider>
      </ConnectionScopeProvider>
    </MemoryRouter>,
  )
}

export const NEVER = '0001-01-01T00:00:00Z'

export function summary(id: string, name: string) {
  return { id, name, engine: 'postgres', database: `${name}_db`, role: `${name}_ro`, mode: 'assisted' }
}

export function detail(id: string, name: string, auditedAt: string, findings: Finding[]) {
  return {
    ...summary(id, name),
    audited_privileges: { audited_at: auditedAt, findings },
    catalog_status: { path: '.keeper/catalog.yaml', fresh: true, freshness_known: true, unclassified: 0 },
    detection: [{ kind: 'patterns' }],
    limits: { max_rows_ceiling: 1000, statement_timeout: 30000000000, scan_sample: 300 },
    has_write_credential: false,
  }
}

export function finding(id: string, detailText: string, narrower: string): Finding {
  return { id, kind: 'relation-write', subject: id, detail: detailText, narrower }
}
