import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, doctorReport, fakeDaemon, finding, renderApp, summary } from './daemon'

const limits = { max_rows_ceiling: 1000, max_bytes: 1048576, statement_timeout: 30000000000, scan_sample: 300 }

function health(id: string, name: string, state: string, extra: Record<string, unknown> = {}) {
  return { id, name, state, findings: 0, catalog_fresh: true, catalog_freshness_known: true, mode: 'assisted', limits, ...extra }
}

function connections(doctor?: unknown[]) {
  const d = fakeDaemon()
  d.on('GET', '/v1/hosts', () => [
    200,
    [
      { id: 'h1', name: 'prod-db', address: 'db.internal', port: 5432, sslmode: 'require', connections: ['c1', 'c2'] },
      { id: 'h2', name: 'stage-db', address: 'stage.internal', port: 5432, sslmode: 'require', connections: ['c3'] },
    ],
  ])
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'orders'), summary('c2', 'billing'), summary('c3', 'staging')]])
  // A doctor that never answers is a fetch that never resolves.
  if (doctor) d.on('GET', '/v1/doctor', () => [200, doctorReport({ connections: doctor })])
  return d
}

const row = async (name: string) => (await screen.findByRole('link', { name })).closest('tr') as HTMLElement

describe('the Connections list', () => {
  test('POL-C1 a lamp shows whether its connection is reachable', async () => {
    connections([health('c1', 'orders', 'reachable'), health('c2', 'billing', 'unreachable'), health('c3', 'staging', 'reachable')])
    renderApp('/connections')
    await waitFor(async () => expect(within(await row('orders')).getByRole('img').getAttribute('data-state')).toBe('live'))
    const down = within(await row('billing')).getByRole('img')
    expect(down.getAttribute('data-state')).toBe('blocked')
    expect(down.getAttribute('aria-label')).toMatch(/unreachable/i)
  })

  test('POL-C2 a connection nobody has checked is neither green nor amber', async () => {
    connections([health('c1', 'orders', 'unknown')])
    renderApp('/connections')
    for (const name of ['orders', 'billing']) {
      const lamp = within(await row(name)).getByRole('img')
      expect(lamp.getAttribute('data-state')).toBe('idle')
    }
  })

  test('POL-C3 every connection sits in one table under a row for its host', async () => {
    connections([])
    renderApp('/connections')
    await screen.findByRole('link', { name: 'orders' })
    const tables = screen.getAllByRole('table')
    expect(tables).toHaveLength(1)
    const rows = within(tables[0]).getAllByRole('row').map((r) => r.textContent ?? '')
    const at = (s: string) => rows.findIndex((r) => r.includes(s))
    expect(at('prod-db')).toBeLessThan(at('orders'))
    expect(at('orders')).toBeLessThan(at('stage-db'))
    expect(at('stage-db')).toBeLessThan(at('staging'))
  })

  test('POL-C4 a filter that matches nothing says so', async () => {
    connections([])
    renderApp('/connections')
    await screen.findByRole('link', { name: 'orders' })
    await userEvent.type(screen.getByRole('searchbox', { name: /filter/i }), 'zzz')
    expect(screen.getByText(/no connection matches/i)).toBeTruthy()
    expect(screen.queryByText('prod-db')).toBeNull()
  })
})

function audited(findings: ReturnType<typeof finding>[]) {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', '2026-10-06T12:00:00Z', findings)])
  return d
}

const writeOn = (t: string) =>
  finding(`relation-write:public.${t}`, `This role can change rows in public.${t}.`, `REVOKE INSERT, UPDATE, DELETE ON public.${t} FROM app_ro;`)

describe('the Privileges tab', () => {
  test('POL-C5 findings are grouped by kind with one statement per group and one script for all', async () => {
    const user = userEvent.setup()
    const schema = { ...finding('schema-create:public', 'This role can create objects in schema public.', 'REVOKE CREATE ON SCHEMA public FROM app_ro;'), kind: 'schema-create' }
    audited([writeOn('orders'), writeOn('users'), writeOn('items'), schema])
    renderApp('/connections/c1/privileges')
    const p = within(await screen.findByRole('region', { name: 'Privileges' }))
    const groups = p.getAllByRole('heading', { level: 3 }).map((h) => h.textContent ?? '')
    expect(groups[0]).toMatch(/schema-create/)
    expect(groups[1]).toMatch(/relation-write.*3/)
    const writes = p.getByLabelText('relation-write statements')
    for (const t of ['orders', 'users', 'items']) expect(writes.textContent).toContain(`public.${t}`)

    await user.click(p.getByRole('button', { name: /copy all fixes/i }))
    const script = await navigator.clipboard.readText()
    expect(script.trim().startsWith('BEGIN;')).toBe(true)
    expect(script.trim().endsWith('COMMIT;')).toBe(true)
    for (const s of ['public.orders', 'public.users', 'public.items', 'CREATE ON SCHEMA public']) expect(script).toContain(s)
  })

  test('POL-C6 a finding no statement removes is set apart and left out of the script', async () => {
    const user = userEvent.setup()
    const vendor = { ...finding('security-definer:pg_catalog.azure_fn', 'A vendor function runs with its owner\'s rights.', ''), kind: 'security-definer', narrower: undefined }
    audited([writeOn('orders'), vendor])
    renderApp('/connections/c1/privileges')
    const p = within(await screen.findByRole('region', { name: 'Privileges' }))
    expect(p.getByRole('heading', { level: 3, name: /no single statement removes/i })).toBeTruthy()
    await user.click(p.getByRole('button', { name: /copy all fixes/i }))
    const script = await navigator.clipboard.readText()
    expect(script).toContain('public.orders')
    expect(script).not.toContain('azure_fn')
  })
})
