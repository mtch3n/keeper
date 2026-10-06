import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, fakeDaemon, finding, NEVER, renderApp, summary } from './daemon'

const AUDITED = '2026-10-06T12:00:00Z'

function oneConnection(auditedAt: string, findings = [finding('relation-write:public.orders', 'This role can change rows in public.orders.', 'REVOKE INSERT ON public.orders FROM app_ro;')]) {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', auditedAt, findings)])
  return d
}

async function privileges() {
  return within(await screen.findByRole('region', { name: 'Privileges' }))
}

describe('a connection\'s Privileges tab', () => {
  test('PRIV-C1 each finding shows what it means and how to narrow it', async () => {
    oneConnection(AUDITED, [
      finding('relation-write:public.orders', 'This role can change rows in public.orders.', 'REVOKE INSERT ON public.orders FROM app_ro;'),
      finding('rolsuper', 'This role is a superuser.', 'ALTER ROLE app_ro NOSUPERUSER;'),
    ])
    renderApp('/connections/c1/privileges')
    const p = await privileges()
    expect(p.getByText('This role can change rows in public.orders.')).toBeTruthy()
    expect(p.getByText('REVOKE INSERT ON public.orders FROM app_ro;')).toBeTruthy()
    expect(p.getByText('This role is a superuser.')).toBeTruthy()
    expect(p.getByText('ALTER ROLE app_ro NOSUPERUSER;')).toBeTruthy()
  })

  test('PRIV-C2 a clean audit says the role holds nothing to report', async () => {
    oneConnection(AUDITED, [])
    renderApp('/connections/c1/privileges')
    expect((await privileges()).getByText(/holds nothing keeper would report/i)).toBeTruthy()
  })

  test('PRIV-C3 an audit that never ran is not reported as clean', async () => {
    oneConnection(NEVER, [])
    renderApp('/connections/c1/privileges')
    const p = await privileges()
    expect(p.getByText(/no report yet/i)).toBeTruthy()
    expect(p.queryByText(/holds nothing keeper would report/i)).toBeNull()
  })

  test('PRIV-C4 Re-audit runs the audit and shows the new findings', async () => {
    const d = oneConnection(AUDITED, [])
    let audited = false
    d.on('POST', '/v1/connections/c1/audit', () => {
      audited = true
      return [200, {}]
    })
    d.on('GET', '/v1/connections/c1', () => [
      200,
      detail('c1', 'one', AUDITED, audited ? [finding('rolsuper', 'This role is a superuser.', 'ALTER ROLE app_ro NOSUPERUSER;')] : []),
    ])
    renderApp('/connections/c1/privileges')
    const p = await privileges()
    await userEvent.click(p.getByRole('button', { name: /re-audit/i }))
    expect(await p.findByText('This role is a superuser.')).toBeTruthy()
    expect(audited).toBe(true)
  })

  test('PRIV-C5 a failed re-audit shows the error and keeps the findings', async () => {
    const d = oneConnection(AUDITED)
    d.on('POST', '/v1/connections/c1/audit', () => [500, { code: 'internal', summary: 'the audit could not run' }])
    renderApp('/connections/c1/privileges')
    const p = await privileges()
    await userEvent.click(p.getByRole('button', { name: /re-audit/i }))
    expect(await screen.findByText(/the audit could not run/)).toBeTruthy()
    expect(p.getByText('This role can change rows in public.orders.')).toBeTruthy()
  })
})

describe('one place, not two', () => {
  test('PRIV-C6 there is no Audit page and no Audit entry', async () => {
    oneConnection(AUDITED)
    renderApp('/connections')
    await screen.findByRole('heading', { name: 'Connections' })
    expect(screen.queryByRole('link', { name: 'Audit' })).toBeNull()
    renderApp('/audit')
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Audit' })).toBeNull())
  })
})
