import { screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import { fakeDaemon, renderApp, session } from './daemon'

const needed = { code: 'ui_session_needed', summary: 'this browser has not opened the keeper dashboard', action: 'run `keeper ui` in your terminal' }

describe('a browser without the dashboard session', () => {
  test('a ticket link says to run keeper ui for that ticket and offers no decision', async () => {
    const d = fakeDaemon()
    d.on('GET', '/v1/approvals/t1', () => [401, needed])
    d.on('GET', '/v1/requests/t1', () => [404, { code: 'ticket_unknown', summary: 'no local request' }])
    renderApp('/r/t1')
    expect(await screen.findByText('keeper ui /r/t1')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
    expect(d.calls.some((c) => c.startsWith('POST'))).toBe(false)
  })

  test('the input page opens by its id alone', async () => {
    const d = fakeDaemon()
    d.on('GET', '/v1/approvals/req1', () => [401, needed])
    d.on('GET', '/v1/requests/req1', () => [
      200,
      {
        request_id: 'req1', kind: 'input', connection_id: 'c1', namespace: 'email', purpose: 'find orders',
        session: session('s1', 'claude-code', '/w'), state: 'pending_input', expires_at: '2099-01-01T00:00:00Z',
      },
    ])
    renderApp('/r/req1')
    expect(await screen.findByText(/A value keeper will not show the agent/)).toBeTruthy()
    expect(screen.queryByText(/keeper ui/)).toBeNull()
  })

  test('the dashboard says to run keeper ui when the daemon restarted', async () => {
    const d = fakeDaemon()
    d.on('GET', '/v1/approvals', () => [401, needed])
    renderApp('/inbox')
    expect(await screen.findByText('keeper ui')).toBeTruthy()
    expect(screen.getByText(/run keeper ui in your terminal/i)).toBeTruthy()
  })
})

