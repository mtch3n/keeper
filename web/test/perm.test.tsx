import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, fakeDaemon, renderApp, session, summary } from './daemon'

const s1 = session('s1', 'claude-code', '/home/u/shop')

function writeCard() {
  return {
    ticket_id: 't1',
    session: s1,
    connection: 'c1',
    facts: {
      statement_type: 'UPDATE',
      relations: [{ schema: 'public', relation: 'orders' }],
      estimated_rows: 3,
      estimated_cost: 1,
      risk: 'write',
      protection: { writes: 'ask', role_writes: 'yes' },
    },
    sql: "UPDATE orders SET status = 'closed'",
    created_at: '2026-10-06T12:00:00Z',
    write: { operation: 'UPDATE', row_count: 3, previewed_at: '2026-10-06T12:00:00Z', scope: [] },
  }
}

test('PERM-C19 a vault with old Findings opens, and an Inbox write card renders with no claim about the role', async () => {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [
    200,
    detail('c1', 'one', {
      audited_privileges: { audited_at: '2026-10-06T12:00:00Z', findings: [{ id: 'f', kind: 'attribute', subject: 'x', detail: 'superuser' }] },
    }),
  ])
  d.on('GET', '/v1/approvals', () => [200, [writeCard()]])
  renderApp('/inbox')
  const card = within(await screen.findByRole('article', { name: /claude-code/ }))
  expect(card.getByText(/3 rows affected/)).toBeTruthy()
  expect(card.queryByText(/role|protection|superuser/i)).toBeNull()
  expect(card.queryByText(/tier/i)).toBeNull()

  renderApp('/connections/c1')
  const nav = within(await screen.findByRole('navigation', { name: 'Connection sections' }))
  expect(nav.getAllByRole('link').map((l) => l.textContent)).toEqual(['Overview', 'Settings', 'Catalog'])
  expect(screen.queryByText(/privilege|finding/i)).toBeNull()
})

function editable() {
  const d = fakeDaemon()
  const sent: unknown[] = []
  let stored: Record<string, unknown> = {}
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', stored)])
  d.on('PATCH', '/v1/connections/c1', (b) => {
    sent.push(b)
    stored = { ...stored, ...(b as object) }
    return [200, {}]
  })
  return sent
}

const region = async (name: string) => within(await screen.findByRole('region', { name }))

describe('the three settings on a connection', () => {
  test('Reads saves through PATCH, and Never asks first because it widens', async () => {
    const sent = editable()
    renderApp('/connections/c1/settings')
    const reads = await region('Reads')
    expect(reads.getAllByRole('radio').map((r) => r.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false'])
    await userEvent.click(reads.getByRole('radio', { name: /^Always/ }))
    expect(await reads.findByText('Saved')).toBeTruthy()
    expect(sent).toEqual([{ reads: 'always' }])
    await userEvent.click(reads.getByRole('radio', { name: /^Never/ }))
    expect(sent).toHaveLength(1)
    await userEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: /never ask/i }))
    await waitFor(() => expect(sent).toEqual([{ reads: 'always' }, { reads: 'never' }]))
    expect(reads.getByText(/skips the question/i).textContent).toMatch(/still blocks database writes/i)
  })

  test('Writes saves through PATCH, and Allow asks first because it commits with no question', async () => {
    const sent = editable()
    renderApp('/connections/c1/settings')
    const writes = await region('Writes')
    expect(writes.getAllByRole('radio').map((r) => r.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false'])
    await userEvent.click(writes.getByRole('radio', { name: /^Allow/ }))
    await userEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: /allow writes/i }))
    await waitFor(() => expect(sent).toEqual([{ writes: 'allow' }]))
    await userEvent.click(writes.getByRole('radio', { name: /^Ask/ }))
    await waitFor(() => expect(sent).toEqual([{ writes: 'allow' }, { writes: 'ask' }]))
    expect(writes.getByText(/commits with no question/i)).toBeTruthy()
  })

  test('Detection saves through PATCH as off, scan or all', async () => {
    const sent = editable()
    renderApp('/connections/c1/settings')
    const det = await region('Detection')
    expect(det.getAllByRole('radio').map((r) => r.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false'])
    await userEvent.click(det.getByRole('radio', { name: /^All columns/ }))
    expect(await det.findByText('Saved')).toBeTruthy()
    await userEvent.click(det.getByRole('radio', { name: /^Off/ }))
    await waitFor(() => expect(sent).toEqual([{ detection: 'all' }, { detection: 'off' }]))
  })
})

describe('what the UI no longer has', () => {
  test('the removed connection tabs are not routes', async () => {
    editable()
    for (const tab of ['detection', 'limits', 'privileges']) {
      const { unmount } = renderApp(`/connections/c1/${tab}`)
      expect(await screen.findByText('Page not found')).toBeTruthy()
      unmount()
    }
  })

  test('the catalog editor offers allow, token, redact and scan and no form or name controls', async () => {
    const d = fakeDaemon()
    d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
    d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one')])
    d.on('GET', '/v1/catalog/c1', () => [
      200,
      { connection_id: 'c1', entries: { 'public.users.email': { policy: 'token', namespace: 'email' } }, unclassified: [], unclassified_count: 0 },
    ])
    renderApp('/connections/c1/catalog')
    await userEvent.click(await screen.findByRole('combobox', { name: 'Policy for public.users.email' }))
    expect((await screen.findAllByRole('option')).map((o) => o.textContent)).toEqual(['allow', 'token', 'redact', 'scan'])
    expect(screen.queryByRole('columnheader', { name: /^(form|name)$/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /suggested grants/i })).toBeNull()
  })
})
