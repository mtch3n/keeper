import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { fakeDaemon, renderApp, session, summary } from './daemon'

const item = {
  ticket_id: 't1',
  session: session('s1', 'claude-code', '/home/u/shop', 'close stale orders'),
  connection: 'c1',
  facts: {
    intent: 'close stale orders',
    statement_type: 'UPDATE',
    relations: [{ schema: 'public', relation: 'orders' }],
    estimated_rows: 3,
    estimated_cost: 1,
    reasons: ['writes=ask'],
    risk: 'write',
  },
  sql: "UPDATE orders SET status = 'closed'",
  created_at: '2026-10-06T12:00:00Z',
  write: {
    operation: 'UPDATE',
    row_count: 1,
    previewed_at: '2026-10-06T12:00:00Z',
    scope: [],
    changes: { columns: ['id', 'status'], rows: [{ old: ['1', 'open'], new: ['1', 'closed'] }] },
  },
}

function daemon() {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  return d
}

describe('the ticket page', () => {
  test('ASK-C19 a ticket link shows its facts and rows and approving decides it', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals/t1', () => [200, item])
    d.on('POST', '/v1/approvals/t1/decide', () => [200, { ok: true }])
    renderApp('/r/t1')
    expect(await screen.findByRole('table', { name: 'Changed rows' })).toBeTruthy()
    expect(screen.getByText(/UPDATE orders SET status/)).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: 'Approve' }))
    expect(await screen.findByText(/you can close this page/i)).toBeTruthy()
    expect(d.calls).toContain('POST /v1/approvals/t1/decide')
  })

  test('ASK-C20 a settled or unknown ticket link says so and offers no decision', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals/gone', () => [404, { code: 'ticket_unknown', summary: 'no ticket with that id' }])
    d.on('GET', '/v1/requests/gone', () => [404, { code: 'ticket_unknown', summary: 'no local request with that id' }])
    renderApp('/r/gone')
    expect(await screen.findByText(/already settled or does not exist/i)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Refuse' })).toBeNull()
    expect(d.calls.some((c) => c.startsWith('POST'))).toBe(false)
  })

  test('the ticket page shows no tier text', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals/t1', () => [200, item])
    renderApp('/r/t1')
    expect(await screen.findByRole('article')).toBeTruthy()
    expect(screen.queryByText(/tier/i)).toBeNull()
  })
})
