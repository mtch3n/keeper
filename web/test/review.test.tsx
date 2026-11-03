import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, doctorReport, emit, fakeDaemon, renderApp, session, summary } from './daemon'
import { streams } from './setup'

import { ScannedText } from '@/components/wrappers/ScannedText'
import { shareEvents, type TabEnv } from '@/lib/events'
import type { KeeperEvent } from '@/lib/types'

const s1 = session('s1', 'claude-code', '/home/u/shop')

const ticket = {
  ticket_id: 't1',
  session: s1,
  connection: 'c1',
  facts: { statement_type: 'UPDATE', relations: [{ schema: 'public', relation: 'orders' }], estimated_rows: 3, estimated_cost: 1, risk: 'write' },
  sql: 'UPDATE orders SET status = 1',
  created_at: '2026-10-06T12:00:00Z',
}

function daemon() {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/hosts', () => [200, [{ id: 'h1', name: 'prod-db', address: 'db.internal', port: 5432, sslmode: 'require', connections: ['c1'] }]])
  d.on('GET', '/v1/doctor', () => [200, doctorReport()])
  return d
}

describe('one event stream', () => {
  test('a page with the shell, the inbox and the agents indicator opens one EventSource', async () => {
    daemon()
    renderApp('/inbox')
    await screen.findByText(/nothing is waiting/i)
    expect(streams.length).toBe(1)
  })

  test('the Inbox reloads when the stream opens again', async () => {
    const d = daemon()
    renderApp('/inbox')
    await screen.findByText(/nothing is waiting/i)
    const before = d.calls.filter((c) => c === 'GET /v1/approvals').length
    act(() => streams[0].onopen?.())
    await waitFor(() => expect(d.calls.filter((c) => c === 'GET /v1/approvals').length).toBeGreaterThan(before))
  })

  test('an approval event reloads the queue instead of reading rows from the event', async () => {
    let queue: unknown[] = []
    const d = daemon()
    d.on('GET', '/v1/approvals', () => [200, queue])
    renderApp('/inbox')
    await screen.findByText(/nothing is waiting/i)
    queue = [ticket]
    emit('approval', { action: 'queued' })
    expect(await screen.findByRole('article', { name: /claude-code/ })).toBeTruthy()
  })
})

function tabs() {
  const held = new Set<string>()
  const waiting: (() => void)[] = []
  const channels: { onmessage: ((m: MessageEvent) => void) | null }[] = []
  const opened: number[] = []
  const feeds: ((e: KeeperEvent) => void)[] = []
  const lock = async (cb: () => Promise<void> | undefined, signal: AbortSignal) => {
    if (held.has('l')) await new Promise<void>((go) => waiting.push(go))
    if (signal.aborted) return
    held.add('l')
    await cb()
    held.delete('l')
    waiting.shift()?.()
  }
  let n = 0
  const env = (): TabEnv => {
    const id = n++
    const mine: { onmessage: ((m: MessageEvent) => void) | null } = { onmessage: null }
    channels.push(mine)
    return {
      locks: { request: ((_: string, o: { signal: AbortSignal }, cb: () => Promise<void> | undefined) => lock(cb, o.signal)) as never },
      channel: () => ({
        get onmessage() {
          return mine.onmessage
        },
        set onmessage(f) {
          mine.onmessage = f
        },
        postMessage: (data: unknown) => channels.filter((c) => c !== mine).forEach((c) => c.onmessage?.({ data } as MessageEvent)),
        close: () => {
          channels.splice(channels.indexOf(mine), 1)
        },
      }),
      connect: (onEvent) => {
        opened.push(id)
        feeds.push(onEvent)
        return () => {}
      },
    }
  }
  return { env, opened, feeds }
}

describe('streams across tabs', () => {
  test('of two tabs only one opens the stream, and the other receives its events', async () => {
    const t = tabs()
    const got: KeeperEvent[] = []
    const a = shareEvents(() => {}, () => {}, t.env())
    const b = shareEvents((e) => got.push(e), () => {}, t.env())
    await waitFor(() => expect(t.opened).toEqual([0]))
    t.feeds[0]({ kind: 'approval', data: {} })
    expect(got.length).toBe(1)
    a()
    await waitFor(() => expect(t.opened).toEqual([0, 1]))
    b()
  })
})

describe('the ticket page follows the queue', () => {
  test('it shows the host, and a ticket another surface decided is replaced by a closed notice', async () => {
    let live = true
    const d = daemon()
    d.on('GET', '/v1/approvals/t1', () => (live ? [200, ticket] : [404, { code: 'ticket_unknown', summary: 'gone' }]))
    renderApp('/r/t1')
    expect(await screen.findByText(/prod-db \(db.internal:5432\)/)).toBeTruthy()
    live = false
    emit('approval', { action: 'decided' })
    expect(await screen.findByText(/no longer waiting/i)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
  })

  test('approving says the statement is running and not that the agent has its answer', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals/t1', () => [200, ticket])
    d.on('POST', '/v1/approvals/t1/decide', () => [200, { ok: true }])
    renderApp('/r/t1')
    await userEvent.click(await screen.findByRole('button', { name: 'Approve' }))
    expect(await screen.findByText('Approved. The statement is running.')).toBeTruthy()
  })
})

describe('the input page', () => {
  const view = (state: string, expires_at: string) => ({
    request_id: 'r1', kind: 'input', connection_id: 'c1', namespace: 'email', purpose: 'p', session: s1, state, expires_at,
  })

  test('an answered request shows no form after a reload', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals/r1', () => [404, { code: 'ticket_unknown', summary: 'x' }])
    d.on('GET', '/v1/requests/r1', () => [200, view('ready', new Date(Date.now() + 60_000).toISOString())])
    renderApp('/r/r1')
    expect(await screen.findByText(/already answered/i)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /confirm and continue/i })).toBeNull()
  })

  test('expiry counts down to the future time', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals/r1', () => [404, { code: 'ticket_unknown', summary: 'x' }])
    d.on('GET', '/v1/requests/r1', () => [200, view('pending_input', new Date(Date.now() + 5 * 60_000).toISOString())])
    renderApp('/r/r1')
    expect(await screen.findByText(/in [45]m/)).toBeTruthy()
  })
})

describe('the Inbox errors', () => {
  test('a first-load failure shows its error and its action instead of a skeleton', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals', () => [500, { code: 'internal', summary: 'the daemon failed', action: 'Restart it.' }])
    renderApp('/inbox')
    expect(await screen.findByText('the daemon failed Restart it.')).toBeTruthy()
  })

  test('a decision failure is cleared when the next decision starts', async () => {
    let fail = true
    const d = daemon()
    d.on('GET', '/v1/approvals', () => [200, [ticket]])
    d.on('POST', '/v1/approvals/t1/decide', () => (fail ? [409, { code: 'ticket_unknown', summary: 'already decided' }] : [200, { ok: true }]))
    renderApp('/inbox')
    const card = within(await screen.findByRole('article', { name: /claude-code/ }))
    await userEvent.click(card.getByRole('button', { name: 'Approve' }))
    expect(await screen.findByText('already decided')).toBeTruthy()
    fail = false
    await userEvent.click(card.getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(screen.queryByText('already decided')).toBeNull())
  })
})

describe('Activity', () => {
  const rec = { id: 'a1', at: '2026-10-06T12:00:00Z', session_id: 's1', client: s1.client, connection: 'c1', statement: 'UPDATE orders', statement_type: 'UPDATE', row_count: 2, duration: 0 }

  test('the Decision filter is sent to the server', async () => {
    const d = daemon()
    d.on('GET', '/v1/activity', () => [200, [rec]])
    renderApp('/activity')
    await screen.findByRole('table')
    await userEvent.click(screen.getByRole('combobox', { name: 'Decision' }))
    await userEvent.click(await screen.findByRole('option', { name: 'refused' }))
    await waitFor(() => expect(d.calls.some((c) => c.includes('/v1/activity?') && c.includes('decision=refused'))).toBe(true))
  })

  test('a write shows the rows it changed, and a size cut says size', async () => {
    const d = daemon()
    d.on('GET', '/v1/activity', () => [200, [rec]])
    d.on('GET', '/v1/activity/a1', () => [200, { ...rec, result: { rows: [], columns: [], row_count: 0, executed_rows: 2, transforms: {}, audit_id: 'a1' } }])
    renderApp('/activity')
    await userEvent.click(await screen.findByText('UPDATE orders'))
    expect(await screen.findByText('2 row(s) changed')).toBeTruthy()
  })

  test('a result cut by size says the size cap', async () => {
    const d = daemon()
    d.on('GET', '/v1/activity', () => [200, [{ ...rec, statement: 'SELECT 1', row_count: 1 }]])
    d.on('GET', '/v1/activity/a1', () => [200, { ...rec, result: { rows: [[1]], columns: [{ name: 'n', type: 'int4', policy: 'allow' }], row_count: 1, truncated: true, truncated_by: 'size', transforms: {}, audit_id: 'a1' } }])
    renderApp('/activity')
    await userEvent.click(await screen.findByText('SELECT 1'))
    expect(await screen.findByText(/truncated at the size cap/)).toBeTruthy()
  })
})

describe('scanned text', () => {
  test('a span the daemon redacted is marked', () => {
    render(<ScannedText text="mail ⟨redacted:email_address⟩ now" />)
    expect(screen.getByLabelText('redacted span').textContent).toBe('⟨redacted:email_address⟩')
  })
})

describe('Settings and connection pages', () => {
  test('the retention fact follows a saved retention', async () => {
    let days = 30
    const d = daemon()
    d.on('GET', '/v1/settings', () => [200, { log_retention_days: days, connection_idle_minutes: 5 }])
    d.on('PUT', '/v1/settings', (body) => {
      days = (body as { log_retention_days: number }).log_retention_days
      return [200, { log_retention_days: days, connection_idle_minutes: 5 }]
    })
    d.on('GET', '/v1/doctor', () => [200, doctorReport({ log_retention_days: days })])
    renderApp('/settings')
    expect(await screen.findByText(/kept 30 day/)).toBeTruthy()
    const input = await screen.findByLabelText(/keep the activity log/i)
    await userEvent.clear(input)
    await userEvent.type(input, '14')
    await userEvent.click(within(input.parentElement!).getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/kept 14 day/)).toBeTruthy()
  })

  test('a connection that fails to load for another reason shows that error', async () => {
    const d = daemon()
    d.on('GET', '/v1/connections/c1', () => [500, { code: 'internal', summary: 'the vault failed' }])
    renderApp('/connections/c1')
    expect(await screen.findByText('the vault failed')).toBeTruthy()
    expect(screen.queryByText(/no such connection/i)).toBeNull()
  })

  test('the host dialog defaults to require, and to prefer for a loopback address', async () => {
    const d = daemon()
    d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one')])
    renderApp('/connections')
    await userEvent.click(await screen.findByRole('button', { name: 'Add a host' }))
    const tls = await screen.findByRole('combobox', { name: /TLS/ })
    expect(tls.textContent).toContain('prefer')
    const address = screen.getByLabelText('Address')
    await userEvent.clear(address)
    await userEvent.type(address, 'db.example.com')
    expect(tls.textContent).toContain('require')
  })

  test('a catalog proposal shows what it could not consult', async () => {
    const d = daemon()
    d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one')])
    d.on('GET', '/v1/catalog/c1', () => [200, { connection_id: 'c1', entries: {}, unclassified_count: 0 }])
    d.on('POST', '/v1/catalog/c1/init', () => [200, { safe_to_bulk_accept: {}, needs_review: {}, degradations: [{ layer: 'detector', reason: 'not running' }] }])
    renderApp('/connections/c1/catalog')
    await userEvent.click(await screen.findByRole('button', { name: 'Propose classifications' }))
    expect(await screen.findByText('detector: not running')).toBeTruthy()
  })
})

describe('plain words on the pages', () => {
  test('a reason code is shown as a sentence and an unknown code as itself', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals', () => [200, [{ ...ticket, facts: { ...ticket.facts, reasons: ['volatile_call', 'new_code'] } }]])
    renderApp('/inbox')
    expect(await screen.findByText(/calls a function that may change data\. new_code/)).toBeTruthy()
  })

  test('the input page names the connection and not its id', async () => {
    const d = daemon()
    d.on('GET', '/v1/approvals/r1', () => [404, { code: 'ticket_unknown', summary: 'x' }])
    d.on('GET', '/v1/requests/r1', () => [200, { request_id: 'r1', kind: 'input', connection_id: 'c1', session: s1, state: 'pending_input', expires_at: new Date(Date.now() + 60_000).toISOString() }])
    renderApp('/r/r1')
    expect(await screen.findByText('one')).toBeTruthy()
  })

  test('registering a database offers Off, Ask and Allow with Off chosen, and no preset catalog path', async () => {
    daemon()
    renderApp('/connections')
    await userEvent.click(await screen.findByRole('button', { name: 'Add a database' }))
    expect((screen.getByRole('radio', { name: /Off/ }) as HTMLInputElement).getAttribute('aria-checked')).toBe('true')
    expect(screen.getByRole('radio', { name: /Ask/ })).toBeTruthy()
    expect(screen.getByRole('radio', { name: /Allow/ })).toBeTruthy()
    expect((screen.getByLabelText(/Catalog path \(optional\)/) as HTMLInputElement).value).toBe('')
  })
})

describe('what the agent received, in the Activity record', () => {
  const rec = { id: 'a1', at: '2026-10-06T12:00:00Z', session_id: 's1', client: s1.client, connection: 'c1', statement: 'SELECT secret', statement_type: 'SELECT', row_count: 1, duration: 0 }

  test('a redacted cell shows the marker, no block glyph, and the header names the policy and links to the catalog', async () => {
    const d = daemon()
    d.on('GET', '/v1/activity', () => [200, [rec]])
    d.on('GET', '/v1/activity/a1', () => [200, { ...rec, result: { rows: [['⟨redacted⟩', 'x']], columns: [{ name: 'secret', type: 'text', policy: 'redact' }, { name: 'id', type: 'text', policy: 'allow' }], row_count: 1, transforms: {}, audit_id: 'a1' } }])
    renderApp('/activity')
    await userEvent.click(await screen.findByText('SELECT secret'))
    expect((await screen.findAllByText('⟨redacted⟩')).length).toBeGreaterThan(0)
    expect(document.body.textContent).not.toContain('█')
    expect(screen.getByText('redact')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'secret' }).getAttribute('href')).toBe('/connections/c1/catalog?column=secret')
  })

  test('the catalog opens filtered to the column the link names', async () => {
    const d = daemon()
    d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one')])
    d.on('GET', '/v1/catalog/c1', () => [200, { connection_id: 'c1', entries: { 'public.t.secret': { policy: 'redact' }, 'public.t.id': { policy: 'allow' } }, unclassified_count: 0 }])
    renderApp('/connections/c1/catalog?column=secret')
    expect(await screen.findByText('public.t.secret')).toBeTruthy()
    expect(screen.queryByText('public.t.id')).toBeNull()
  })
})
