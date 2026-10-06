import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'

import { detail, doctorReport, emit, fakeDaemon, renderApp, session, summary } from './daemon'

const s1 = session('s1', 'claude-code', '/home/u/shop', 'reconcile OPS-441')
const s2 = session('s2', 'codex', '/home/u/billing', 'find duplicate invoices')

function pending() {
  return {
    ticket_id: 't1',
    session: s1,
    connection: 'c1',
    tier: 3,
    facts: { intent: 'reconcile OPS-441', statement_type: 'SELECT', relations: [{ schema: 'public', relation: 'orders' }], estimated_rows: 900, estimated_cost: 12, reasons: ['near_row_cap'] },
    sql: 'SELECT id FROM orders',
    created_at: '2026-10-06T12:00:00Z',
  }
}

function daemon(over: { approvals?: unknown[]; sessions?: unknown[] } = {}) {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one'), summary('c2', 'two')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', '2026-10-06T12:00:00Z', [])])
  d.on('GET', '/v1/connections/c2', () => [200, detail('c2', 'two', '2026-10-06T12:00:00Z', [])])
  d.on('GET', '/v1/catalog/c1', () => [200, { connection_id: 'c1', entries: {}, unclassified_count: 0 }])
  d.on('GET', '/v1/catalog/c2', () => [200, { connection_id: 'c2', entries: {}, unclassified_count: 0 }])
  d.on('GET', '/v1/hosts', () => [200, [{ id: 'h1', name: 'prod-db', address: 'db.internal', port: 5432, sslmode: 'require', connections: ['c1', 'c2'] }]])
  d.on('GET', '/v1/approvals', () => [200, over.approvals ?? []])
  d.on('GET', '/v1/requests', () => [200, []])
  d.on('GET', '/v1/doctor', () => [200, doctorReport({ sessions: over.sessions ?? [] })])
  return d
}

const bar = async () => within(await screen.findByRole('navigation', { name: 'Sections' }))

describe('the shell', () => {
  test('SHELL-C1 the top bar holds four sections and there is no sidebar', async () => {
    daemon()
    renderApp('/connections')
    expect((await bar()).getAllByRole('link').map((l) => l.textContent)).toEqual(['Connections', 'Inbox', 'Activity', 'Settings'])
    expect(screen.queryByRole('navigation', { name: 'Databases' })).toBeNull()
  })

  test('SHELL-C2 the agents indicator counts and lists connected agents', async () => {
    daemon({ sessions: [s1, s2] })
    renderApp('/connections')
    const indicator = await screen.findByRole('button', { name: /2 agents/i })
    await userEvent.click(indicator)
    const list = within(await screen.findByRole('dialog'))
    expect(list.getByText('/home/u/shop')).toBeTruthy()
    expect(list.getByText('find duplicate invoices')).toBeTruthy()
  })

  test('SHELL-C3 with no agent connected the indicator says so', async () => {
    daemon()
    renderApp('/connections')
    await userEvent.click(await screen.findByRole('button', { name: /no agents/i }))
    expect(within(await screen.findByRole('dialog')).getByText(/no agent is connected/i)).toBeTruthy()
  })

  test('SHELL-C4 the header picker switches connection and keeps the tab', async () => {
    daemon()
    renderApp('/connections/c1/catalog')
    await screen.findByRole('heading', { name: 'one', level: 1 })
    await userEvent.click(screen.getByRole('combobox', { name: 'Connection' }))
    await userEvent.click(await screen.findByRole('option', { name: 'two' }))
    expect(await screen.findByRole('heading', { name: 'two', level: 1 })).toBeTruthy()
    expect(within(screen.getByRole('navigation', { name: 'Connection sections' })).getByRole('link', { name: 'Catalog' }).getAttribute('aria-current')).toBe('page')
  })
})

describe('the Inbox', () => {
  test('SHELL-C5 a card says who asks, on which profile, why and what', async () => {
    daemon({ approvals: [pending()] })
    renderApp('/inbox')
    const card = within(await screen.findByRole('article', { name: /claude-code/ }))
    for (const text of ['claude-code', '/home/u/shop', 'reconcile OPS-441', 'one', 'db.internal', 'one_ro', 'near_row_cap', 'SELECT id FROM orders']) {
      expect(card.getAllByText(new RegExp(text.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&'))).length).toBeGreaterThan(0)
    }
    expect((await bar()).getByRole('link', { name: /Inbox/ }).textContent).toContain('1')
  })

  test('SHELL-C6 approving moves the item from Inbox to Activity', async () => {
    let decided = false
    const d = daemon({ approvals: [pending()] })
    d.on('GET', '/v1/approvals', () => [200, decided ? [] : [pending()]])
    d.on('POST', '/v1/approvals/t1/decide', () => {
      decided = true
      return [200, { ok: true }]
    })
    d.on('GET', '/v1/activity', () => [200, decided ? [{ id: 'a1', at: '2026-10-06T12:01:00Z', session_id: 's1', client: s1.client, connection: 'c1', statement: 'SELECT id FROM orders', statement_type: 'SELECT', tier: 3, row_count: 4, approver: 'operator', decision: 'approved', duration: 0 }] : []])
    renderApp('/inbox')
    const card = within(await screen.findByRole('article', { name: /claude-code/ }))
    await userEvent.click(card.getByRole('button', { name: 'Approve' }))
    expect(await screen.findByText(/nothing is waiting/i)).toBeTruthy()
    await userEvent.click((await bar()).getByRole('link', { name: 'Activity' }))
    expect(await screen.findByText(/approved by operator/i)).toBeTruthy()
  })

  test('SHELL-C14 an empty Inbox says so and the top bar shows no badge', async () => {
    daemon()
    renderApp('/inbox')
    expect(await screen.findByText(/nothing is waiting/i)).toBeTruthy()
    expect((await bar()).getByRole('link', { name: /Inbox/ }).textContent).toBe('Inbox')
  })
})

describe('notifications', () => {
  function stubNotifications() {
    const shown: string[] = []
    class N {
      static permission = 'granted'
      static requestPermission = async () => 'granted'
      constructor(title: string) {
        shown.push(title)
      }
      close() {}
    }
    vi.stubGlobal('Notification', N)
    Object.defineProperty(document, 'hidden', { value: true, configurable: true })
    return shown
  }

  test('SHELL-C8 a new Inbox item raises a notification when the page is hidden', async () => {
    const shown = stubNotifications()
    daemon()
    renderApp('/connections')
    await bar()
    emit('approval', { action: 'queued', ticket_id: 't1', item: pending() })
    await waitFor(() => expect(shown).toHaveLength(1))
  })

  test('SHELL-C9 notifications turned off raise nothing', async () => {
    const shown = stubNotifications()
    localStorage.setItem('keeper.notifications', 'off')
    daemon()
    renderApp('/connections')
    await bar()
    emit('approval', { action: 'queued', ticket_id: 't1', item: pending() })
    await new Promise((r) => setTimeout(r, 50))
    expect(shown).toHaveLength(0)
  })
})

describe('Activity and Settings', () => {
  const rec = (id: string, connection: string) => ({ id, at: '2026-10-06T12:00:00Z', session_id: 's1', client: s1.client, connection, statement: 'SELECT 1', statement_type: 'SELECT', tier: 0, row_count: 1, duration: 0 })

  test('SHELL-C10 Activity shows connection names and filters by them', async () => {
    const d = daemon()
    d.on('GET', '/v1/activity', () => [200, [rec('a1', 'c1'), rec('a2', 'c2')]])
    renderApp('/activity')
    const table = within(await screen.findByRole('table'))
    expect(await table.findByText('one')).toBeTruthy()
    expect(table.getByText('two')).toBeTruthy()
    await userEvent.click(screen.getByRole('combobox', { name: 'Connection' }))
    const options = (await screen.findAllByRole('option')).map((o) => o.textContent)
    expect(options).toEqual(expect.arrayContaining(['one', 'two']))
  })

  test('SHELL-C15 a record for a removed connection says so', async () => {
    const d = daemon()
    d.on('GET', '/v1/activity', () => [200, [rec('a1', 'gone')]])
    renderApp('/activity')
    expect(await within(await screen.findByRole('table')).findByText('removed connection')).toBeTruthy()
  })

  test('SHELL-C13 Settings lists standing allow rules with revoke', async () => {
    const d = daemon()
    d.on('GET', '/v1/grants', () => [200, [{ id: 'g1', path: { connection_id: 'c1', relation: { schema: 'public', relation: 'orders' } }, lifetime: 'standing', row_ceiling: 100, created_by: 'ming', created_at: '2026-10-06T12:00:00Z', uses: 2 }]])
    renderApp('/settings')
    const perms = within(await screen.findByRole('region', { name: 'Permissions' }))
    expect(await perms.findByText(/public\.orders/)).toBeTruthy()
    expect(perms.getByRole('button', { name: /revoke/i })).toBeTruthy()
  })
})
