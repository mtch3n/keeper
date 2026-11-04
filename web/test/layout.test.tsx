import { screen, within } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { Breakable } from '@/components/wrappers/Breakable'
import { onRelations, outcomeText, plural, rowsText } from '@/lib/render'
import { render } from '@testing-library/react'

import { doctorReport, fakeDaemon, renderApp, session, summary } from './daemon'

const s1 = session('s1', 'claude-code', '/home/u/shop')

describe('copy helpers', () => {
  test('a count is singular at one', () => {
    expect(plural(1, 'row')).toBe('1 row')
    expect(plural(0, 'row')).toBe('0 rows')
    expect(plural(7, 'row')).toBe('7 rows')
  })

  test('a statement that touches no table omits "on"', () => {
    expect(onRelations('SELECT', [])).toBe('SELECT')
    expect(onRelations('SELECT', undefined)).toBe('SELECT')
    expect(onRelations('SELECT', [{ schema: 'public', relation: 'orders' }])).toBe('SELECT on public.orders')
  })

  test('a record says what happened to its statement', () => {
    expect(outcomeText({})).toBe('ran')
    expect(outcomeText({ error_code: 'approval_required' })).toBe('asked')
    expect(outcomeText({ error_code: 'permission_denied' })).toBe('permission_denied')
    expect(outcomeText({ decision: 'approved', approver: 'ana' })).toBe('approved by ana')
    expect(outcomeText({ decision: 'refused', approver: 'ana', error_code: 'approval_refused' })).toBe('refused by ana')
    expect(outcomeText({ decision: 'cancelled', approver: 'ana' })).toBe('cancelled by ana')
    expect(outcomeText({ decision: 'expired', approver: 'keeper' })).toBe('expired')
  })

  test('rows are a dash when the statement did not run', () => {
    expect(rowsText({ row_count: 0, error_code: 'approval_required' })).toBe('—')
    expect(rowsText({ row_count: 0, decision: 'refused', error_code: 'approval_refused' })).toBe('—')
    expect(rowsText({ row_count: 4 })).toBe('4')
    expect(rowsText({ row_count: 1, decision: 'approved' })).toBe('1')
  })

  test('a name may break only after a dot or an underscore', () => {
    const { container } = render(<Breakable text="public.customers_old.street" />)
    expect(container.textContent).toBe('public.customers_old.street')
    expect(container.querySelectorAll('wbr')).toHaveLength(3)
  })
})

describe('Activity decision column', () => {
  test('an escalated statement reads asked with no rows, a direct run reads ran', async () => {
    const base = { at: '2026-10-06T12:00:00Z', session_id: 's1', client: s1.client, connection: 'c1', statement_type: 'DELETE', duration: 0 }
    const d = fakeDaemon()
    d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
    d.on('GET', '/v1/doctor', () => [200, doctorReport()])
    d.on('GET', '/v1/activity', () => [
      200,
      [
        { ...base, id: 'a1', statement: 'DELETE FROM waiting', row_count: 0, error_code: 'approval_required' },
        { ...base, id: 'a2', statement: 'SELECT ran', row_count: 3 },
      ],
    ])
    renderApp('/activity')
    const asked = (await screen.findByText('DELETE FROM waiting')).closest('tr')!
    expect(within(asked).getByText('asked')).toBeTruthy()
    expect(asked.cells[5].textContent).toBe('—')
    const ran = screen.getByText('SELECT ran').closest('tr')!
    expect(within(ran).getByText('ran')).toBeTruthy()
    expect(ran.cells[5].textContent).toBe('3')
  })
})

describe('Inbox impact line', () => {
  test('a read of no table says "SELECT" and "about 1 row"', async () => {
    const d = fakeDaemon()
    d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
    d.on('GET', '/v1/doctor', () => [200, doctorReport()])
    d.on('GET', '/v1/approvals', () => [
      200,
      [{
        ticket_id: 't1', session: s1, connection: 'c1',
        facts: { statement_type: 'SELECT', relations: [], estimated_rows: 1, estimated_cost: 1, risk: 'read' },
        sql: 'SELECT 1', created_at: '2026-10-06T12:00:00Z',
      }],
    ])
    renderApp('/inbox')
    expect(await screen.findByText('SELECT · about 1 row')).toBeTruthy()
  })
})
