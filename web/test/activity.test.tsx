import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { doctorReport, fakeDaemon, renderApp, session, summary } from './daemon'

const s1 = session('s1', 'claude-code', '/home/u/shop')
const s2 = session('s2', 'codex', '/home/u/blog')
const now = new Date().toISOString()

const rec = (id: string, over: Record<string, unknown> = {}) => ({
  id, at: now, session_id: 's1', client: s1.client, connection: 'c1', statement: `SELECT ${id}`,
  statement_type: 'SELECT', row_count: 1, duration: 0, ...over,
})

function daemon(records: unknown[]) {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/doctor', () => [200, doctorReport()])
  d.on('GET', '/v1/activity', () => [200, records])
  return d
}

describe('the Activity log', () => {
  test('records sit under their day and then their session', async () => {
    daemon([rec('a1'), rec('a2', { session_id: 's2', client: s2.client }), rec('a3')])
    renderApp('/activity')
    const table = within(await screen.findByRole('table'))
    expect(await table.findByText('Today')).toBeTruthy()
    const rows = table.getAllByRole('row').map((r) => r.textContent ?? '')
    const at = (text: string) => rows.findIndex((r) => r.includes(text))
    expect(at('claude-code · /home/u/shop')).toBeLessThan(at('SELECT a1'))
    expect(at('SELECT a3')).toBeLessThan(at('codex · /home/u/blog'))
    expect(at('codex · /home/u/blog')).toBeLessThan(at('SELECT a2'))
  })

  test('a record titles itself by what it read and opens from the keyboard', async () => {
    const d = daemon([rec('a1', { relations: [{ schema: 'public', relation: 'orders' }] })])
    d.on('GET', '/v1/activity/a1', () => [200, rec('a1', { statement: 'SELECT id FROM orders' })])
    renderApp('/activity')
    const open = await screen.findByRole('button', { name: 'Read public.orders' })
    expect(open.getAttribute('aria-expanded')).toBe('false')
    open.focus()
    await userEvent.keyboard('{Enter}')
    expect(await screen.findByText('SELECT id FROM orders')).toBeTruthy()
    expect(open.getAttribute('aria-expanded')).toBe('true')
  })

  test('a column keeper did not know reads unclassified and offers the catalog', async () => {
    const r = rec('a1', { relations: [{ schema: 'public', relation: 't' }], transforms: { note: { policy: 'redact', basis: 'unknown' } } })
    const d = daemon([r])
    d.on('GET', '/v1/activity/a1', () => [200, {
      ...r,
      result: { rows: [['⟨redacted⟩']], columns: [{ name: 'note', type: 'text', policy: 'redact' }], row_count: 1, transforms: { note: { policy: 'redact', basis: 'unknown' } }, audit_id: 'a1' },
    }])
    renderApp('/activity')
    await userEvent.click(await screen.findByRole('button', { name: 'Read public.t' }))
    expect(await screen.findByText(/1 column not in the catalog/)).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Classify in Catalog' }).getAttribute('href')).toBe('/connections/c1/catalog?column=note')
    expect(screen.getByRole('link', { name: 'not in the catalog' }).getAttribute('href')).toBe('/connections/c1/catalog?column=note')
    expect(screen.getByLabelText(/unclassified/i)).toBeTruthy()
  })

  test('a system catalog column says keeper does not classify it and offers no link', async () => {
    const r = rec('a1', { relations: [{ schema: 'pg_catalog', relation: 'pg_database' }], transforms: { datname: { policy: 'redact', basis: 'unknown' } } })
    const d = daemon([r])
    d.on('GET', '/v1/activity/a1', () => [200, r])
    renderApp('/activity')
    await userEvent.click(await screen.findByRole('button', { name: 'Read pg_catalog.pg_database' }))
    expect(await screen.findByText(/keeper does not classify system catalogs/)).toBeTruthy()
    expect(screen.queryByRole('link', { name: 'Classify in Catalog' })).toBeNull()
  })
})
