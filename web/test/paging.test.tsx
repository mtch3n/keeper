import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { doctorReport, fakeDaemon, renderApp, session, summary } from './daemon'

const s1 = session('s1', 'claude-code', '/home/u/shop')

const record = (i: number, decision?: string) => ({
  id: `a${i}`, at: '2026-10-06T12:00:00Z', session_id: 's1', client: s1.client, connection: 'c1',
  statement: `SELECT ${i}`, statement_type: 'SELECT', row_count: 1, duration: 0, decision,
})

function activity(n: number, decisionEvery = 0) {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/activity', () => [200, Array.from({ length: n }, (_, i) => record(i, decisionEvery && i % decisionEvery === 0 ? 'refused' : undefined))])
  return d
}

const lastActivityCall = (d: { calls: string[] }) => d.calls.filter((c) => c.includes('/v1/activity?')).at(-1)!
const next = () => screen.getByRole('button', { name: 'Next' })
const previous = () => screen.getByRole('button', { name: 'Previous' })

describe('paging a server list: Activity', () => {
  test('it shows the range and total, disables the ends and asks the server for each page', async () => {
    const d = activity(60)
    renderApp('/activity')
    expect(await screen.findByText('1–25 of 60 records')).toBeTruthy()
    expect((previous() as HTMLButtonElement).disabled).toBe(true)
    await userEvent.click(next())
    expect(await screen.findByText('26–50 of 60 records')).toBeTruthy()
    expect(lastActivityCall(d)).toContain('offset=25')
    await userEvent.click(next())
    expect(await screen.findByText('51–60 of 60 records')).toBeTruthy()
    expect((next() as HTMLButtonElement).disabled).toBe(true)
  })

  test('the page is in the URL, so a reload opens the same page', async () => {
    const d = activity(60)
    renderApp('/activity?activity_page=3')
    expect(await screen.findByText('51–60 of 60 records')).toBeTruthy()
    expect(lastActivityCall(d)).toContain('offset=50')
  })

  test('a page past the end falls back to the last page', async () => {
    activity(60)
    renderApp('/activity?activity_page=9')
    expect(await screen.findByText('51–60 of 60 records')).toBeTruthy()
  })

  test('changing the page size goes back to page 1 and is remembered for the list', async () => {
    const d = activity(60)
    renderApp('/activity?activity_page=3')
    await screen.findByText('51–60 of 60 records')
    await userEvent.click(screen.getByRole('combobox', { name: 'Rows per page' }))
    await userEvent.click(await screen.findByRole('option', { name: '50' }))
    expect(await screen.findByText('1–50 of 60 records')).toBeTruthy()
    expect(lastActivityCall(d)).toContain('limit=50')
    expect(localStorage.getItem('keeper.pagesize.activity')).toBe('50')
  })

  test('changing a filter goes back to page 1 under the filter', async () => {
    const d = activity(60, 2)
    renderApp('/activity?activity_page=2')
    await screen.findByText('26–50 of 60 records')
    await userEvent.click(screen.getByRole('combobox', { name: 'Decision' }))
    await userEvent.click(await screen.findByRole('option', { name: 'refused' }))
    expect(await screen.findByText('1–25 of 30 records')).toBeTruthy()
    expect(lastActivityCall(d)).toContain('decision=refused')
    expect(lastActivityCall(d)).not.toContain('offset=25')
  })

  test('a list that fits on one page shows its count and no page controls', async () => {
    activity(3)
    renderApp('/activity')
    expect(await screen.findByText('1–3 of 3 records')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Next' })).toBeNull()
    expect(screen.queryByRole('combobox', { name: 'Rows per page' })).toBeNull()
  })
})

describe('paging a whole list: the Inbox', () => {
  const read = (i: number) => ({
    ticket_id: `t${String(i).padStart(2, '0')}`, session: s1, connection: 'c1',
    facts: { statement_type: 'SELECT', relations: [], estimated_rows: 1, estimated_cost: 1, risk: 'read' },
    sql: `SELECT ${i}`, created_at: `2026-10-06T12:00:${String(i).padStart(2, '0')}Z`,
  })

  test('deciding the only item on the last page moves back to the page before', async () => {
    let queue = Array.from({ length: 26 }, (_, i) => read(i))
    const d = fakeDaemon()
    d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
    d.on('GET', '/v1/doctor', () => [200, doctorReport()])
    d.on('GET', '/v1/approvals', () => [200, queue])
    d.on('POST', '/v1/approvals/t25/decide', () => {
      queue = queue.slice(0, 25)
      return [200, { ok: true }]
    })
    renderApp('/inbox?inbox_page=2')
    expect(await screen.findByText('26–26 of 26 waiting')).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: 'Approve' }))
    expect(await screen.findByText('1–25 of 25 waiting')).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Next' })).toBeNull())
  })

  test('a write is not decided from its row: Approve appears only once it is open', async () => {
    const d = fakeDaemon()
    d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
    d.on('GET', '/v1/doctor', () => [200, doctorReport()])
    const write = { ...read(1), facts: { ...read(1).facts, risk: 'write' }, write: { operation: 'UPDATE', row_count: 1, previewed_at: '', scope: [] } }
    d.on('GET', '/v1/approvals', () => [200, [write, { ...write, ticket_id: 't02' }]])
    renderApp('/inbox')
    await screen.findByText('1–2 of 2 waiting')
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
    await userEvent.click(screen.getAllByRole('button', { name: 'Review' })[0])
    expect(await screen.findByRole('button', { name: 'Approve' })).toBeTruthy()
  })
})
