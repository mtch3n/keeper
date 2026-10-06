import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, fakeDaemon, finding, renderApp, summary } from './daemon'

const AUDITED = '2026-10-06T12:00:00Z'

/** Two connections on one host; c1 has 3 findings and 7 unclassified columns. */
function daemon() {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one'), summary('c2', 'two'), summary('c3', 'ccpg')]])
  for (const [id, name] of [['c1', 'one'], ['c2', 'two'], ['c3', 'ccpg']]) {
    const findings = id === 'c1' ? [finding('a', 'A.', 'SELECT 1;'), finding('b', 'B.', 'SELECT 2;'), finding('c', 'C.', 'SELECT 3;')] : []
    d.on('GET', `/v1/connections/${id}`, () => [200, detail(id, name, AUDITED, findings)])
    d.on('GET', `/v1/catalog/${id}`, () => [
      200,
      { connection_id: id, entries: {}, unclassified: id === 'c1' ? ['public.t.a', 'public.t.b'] : [], unclassified_count: id === 'c1' ? 7 : 0 },
    ])
  }
  d.on('GET', '/v1/hosts', () => [200, [{ id: 'h1', name: 'db1', address: 'db.internal', port: 5432, sslmode: 'require', connections: ['c1', 'c2', 'c3'] }]])
  d.on('GET', '/v1/doctor', () => [
    200,
    {
      version: 'test', key_source: 'test', pending_approvals: 0, open_tickets: 0, open_requests: 0, grants: 0, suspended_grants: 0,
      connections: [{ id: 'c1', name: 'one', findings: 3, unclassified_columns: 7, catalog_fresh: true, catalog_freshness_known: true, mode: 'assisted', limits: { max_rows_ceiling: 1000, statement_timeout: 30000000000, scan_sample: 300 } }],
    },
  ])
  return d
}

const databases = () => within(screen.getByRole('navigation', { name: 'Databases' }))
const tabs = async () => within(await screen.findByRole('navigation', { name: 'Connection sections' }))

describe('one page per connection', () => {
  test('NAV-C1 picking a database in the sidebar opens its Overview', async () => {
    daemon()
    renderApp('/connections')
    await userEvent.click(await databases().findByRole('link', { name: 'ccpg' }))
    expect(await screen.findByRole('heading', { name: 'ccpg', level: 1 })).toBeTruthy()
    expect((await tabs()).getByRole('link', { name: 'Overview' }).getAttribute('aria-current')).toBe('page')
    expect(databases().getByRole('link', { name: 'ccpg' }).getAttribute('aria-current')).toBe('page')
  })

  test('NAV-C2 picking another database keeps the open tab', async () => {
    daemon()
    renderApp('/connections/c1/detection')
    await (await tabs()).findByRole('link', { name: 'Detection' })
    await userEvent.click(await databases().findByRole('link', { name: 'two' }))
    expect(await screen.findByRole('heading', { name: 'two', level: 1 })).toBeTruthy()
    expect((await tabs()).getByRole('link', { name: 'Detection' }).getAttribute('aria-current')).toBe('page')
  })

  test('NAV-C3 the top bar holds four sections and no Catalog or Policy', async () => {
    daemon()
    renderApp('/connections')
    const bar = within(await screen.findByRole('navigation', { name: 'Sections' }))
    expect(bar.getAllByRole('link').map((l) => l.textContent)).toEqual(['Connections', 'Approvals', 'Activity', 'Permissions'])
  })

  test('NAV-C4 /policy and /catalog are not found', async () => {
    daemon()
    renderApp('/policy')
    expect(await screen.findByText('Page not found')).toBeTruthy()
    renderApp('/catalog')
    expect((await screen.findAllByText('Page not found')).length).toBe(2)
  })

  test('NAV-C5 a deep link opens that tab with the database selected', async () => {
    daemon()
    renderApp('/connections/c1/privileges')
    expect((await tabs()).getByRole('link', { name: 'Privileges' }).getAttribute('aria-current')).toBe('page')
    expect(await screen.findByRole('region', { name: 'Privileges' })).toBeTruthy()
    expect((await databases().findByRole('link', { name: 'one' })).getAttribute('aria-current')).toBe('page')
  })

  test('NAV-C6 an unknown connection says so and links back', async () => {
    const d = daemon()
    d.on('GET', '/v1/connections/nope', () => [404, { code: 'ticket_unknown', summary: 'no connection with that id is registered' }])
    renderApp('/connections/nope')
    expect(await screen.findByText(/no such connection/i)).toBeTruthy()
    expect(screen.getByRole('link', { name: /back to connections/i }).getAttribute('href')).toBe('/connections')
  })

  test('NAV-C7 each tab shows that connection\'s existing editor', async () => {
    daemon()
    renderApp('/connections/c1/detection')
    expect(await screen.findByRole('switch', { name: 'Patterns' })).toBeTruthy()
    await userEvent.click((await tabs()).getByRole('link', { name: 'Catalog' }))
    expect(await screen.findByText(/7 column\(s\) unclassified/)).toBeTruthy()
    await userEvent.click((await tabs()).getByRole('link', { name: 'Limits' }))
    expect(await screen.findByRole('radio', { name: /Assisted/ })).toBeTruthy()
  })

  test('NAV-C8 Overview shows the counts and links each to its tab', async () => {
    daemon()
    renderApp('/connections/c1')
    const findings = await screen.findByRole('link', { name: /3 privilege findings/i })
    expect(findings.getAttribute('href')).toBe('/connections/c1/privileges')
    const unclassified = await screen.findByRole('link', { name: /7 unclassified columns/i })
    expect(unclassified.getAttribute('href')).toBe('/connections/c1/catalog')
  })

  test('NAV-C9 Connections rows and Settings counts open the connection', async () => {
    daemon()
    renderApp('/connections')
    const main = within(await screen.findByRole('main'))
    expect((await main.findByRole('link', { name: 'one' })).getAttribute('href')).toBe('/connections/c1')
    renderApp('/settings')
    const counts = await screen.findAllByRole('link', { name: '3' })
    expect(counts[0].getAttribute('href')).toBe('/connections/c1/privileges')
  })
})

test('a catalog that cannot be read leaves the rest of Overview standing', async () => {
  const d = daemon()
  d.on('GET', '/v1/catalog/c1', () => [500, { code: 'internal', summary: 'the database did not complete this statement' }])
  renderApp('/connections/c1')
  expect(await screen.findByText(/unavailable — the catalog could not be read/)).toBeTruthy()
  expect(await screen.findByText(/db1 · db.internal:5432/)).toBeTruthy()
  expect(screen.queryByText('the database did not complete this statement')).toBeNull()
})
