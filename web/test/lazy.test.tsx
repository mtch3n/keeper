import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, doctorReport, fakeDaemon, renderApp, summary } from './daemon'

function daemon() {
  const d = fakeDaemon()
  const sent: { path: string; body: unknown }[] = []
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', '2026-10-06T12:00:00Z', [])])
  d.on('GET', '/v1/hosts', () => [200, [{ id: 'h1', name: 'prod-db', address: 'db.internal', port: 5432, sslmode: 'require', connections: ['c1'] }]])
  d.on('GET', '/v1/doctor', () => [
    200,
    doctorReport({ connection_idle_minutes: 7, connections: [{ id: 'c1', name: 'one', findings: 2, mode: 'assisted' }] }),
  ])
  d.on('GET', '/v1/settings', () => [200, { log_retention_days: 30, connection_idle_minutes: 7 }])
  d.on('PUT', '/v1/settings', (body) => {
    sent.push({ path: '/v1/settings', body })
    return [200, { log_retention_days: 30, connection_idle_minutes: 9 }]
  })
  d.on('POST', '/v1/connections/c1/test', () => {
    sent.push({ path: '/v1/connections/c1/test', body: null })
    return [200, { user: 'one_ro', server_version: '18.0', round_trip: 4000000 }]
  })
  return sent
}

describe('connections without a status', () => {
  test('LAZY-C5 no page shows reachability, and Test connection says who the account logged in as', async () => {
    const sent = daemon()
    renderApp('/connections')
    await screen.findByRole('link', { name: 'one' })
    expect(screen.queryByRole('img', { name: /reachable|unreachable|no answer|not checked/i })).toBeNull()

    renderApp('/settings')
    await screen.findByRole('heading', { name: 'Settings' })
    await waitFor(() => expect(screen.queryByText(/^(reachable|unreachable|no answer yet)$/i)).toBeNull())

    renderApp('/connections/c1')
    const test_ = await screen.findByRole('button', { name: 'Test connection' })
    expect(sent.some((s) => s.path.endsWith('/test'))).toBe(false)
    await userEvent.click(test_)
    expect(await screen.findByText(/connected as one_ro/i)).toBeTruthy()
    expect(screen.getByText(/PostgreSQL 18\.0/)).toBeTruthy()
  })

  test('LAZY-C11 Settings shows the idle limit, and saving it sends it', async () => {
    const sent = daemon()
    renderApp('/settings')
    const field = await screen.findByLabelText(/close idle database connections after/i)
    await waitFor(() => expect((field as HTMLInputElement).value).toBe('7'))
    await userEvent.clear(field)
    await userEvent.type(field, '9')
    const section = within(field.closest('section') as HTMLElement)
    await userEvent.click(section.getAllByRole('button', { name: 'Save' }).at(-1)!)
    await waitFor(() => expect(sent.find((s) => s.path === '/v1/settings')?.body).toEqual({ connection_idle_minutes: 9 }))
    expect(await section.findByText('Saved')).toBeTruthy()
  })
})
