import { screen, within } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { fakeDaemon, renderApp, session, summary } from './daemon'

const s1 = session('s1', 'claude-code', '/home/u/shop', 'close stale orders')

function write(over: Record<string, unknown> = {}, facts: Record<string, unknown> = {}) {
  return {
    ticket_id: 't1',
    session: s1,
    connection: 'c1',
    tier: 3,
    facts: {
      intent: 'close stale orders',
      statement_type: 'UPDATE',
      relations: [{ schema: 'public', relation: 'orders' }],
      estimated_rows: 3,
      estimated_cost: 1,
      reasons: ['write'],
      ...facts,
    },
    sql: "UPDATE orders SET status = 'closed'",
    created_at: '2026-10-06T12:00:00Z',
    write: { operation: 'UPDATE', row_count: 3, previewed_at: '2026-10-06T12:00:00Z', scope: [], ...over },
  }
}

function rows(n: number) {
  return Array.from({ length: n }, (_, i) => ({ old: [String(i + 1), 'open'], new: [String(i + 1), 'closed'] }))
}

function daemon(approvals: unknown[]) {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/approvals', () => [200, approvals])
  return d
}

const card = async () => within(await screen.findByRole('article', { name: /claude-code/ }))

describe('a waiting write', () => {
  test('WRITE-C4 its card shows the rows affected and each changed row in cleartext', async () => {
    daemon([write({ changes: { columns: ['id', 'status'], rows: rows(3) } })])
    renderApp('/inbox')
    const c = await card()
    expect(c.getByText(/3 rows affected/)).toBeTruthy()
    const table = within(c.getByRole('table', { name: 'Changed rows' }))
    expect(table.getAllByText('closed')).toHaveLength(3)
    expect(table.getAllByText('open')).toHaveLength(3)
  })

  test('WRITE-C5 a large write shows the first 50 rows and says the rest are not shown', async () => {
    daemon([write({ row_count: 500, changes: { columns: ['id', 'status'], rows: rows(50), omitted: 450 } })])
    renderApp('/inbox')
    const c = await card()
    expect(c.getByText(/500 rows affected/)).toBeTruthy()
    expect(within(c.getByRole('table', { name: 'Changed rows' })).getAllByText('closed')).toHaveLength(50)
    expect(c.getByText(/450 more rows are not shown/)).toBeTruthy()
  })
})
