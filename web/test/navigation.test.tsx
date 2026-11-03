import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, fakeDaemon, renderApp, summary } from './daemon'

/** Three connections on one host; c1 has 7 unclassified columns. */
function daemon() {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one'), summary('c2', 'two'), summary('c3', 'ccpg')]])
  for (const [id, name] of [['c1', 'one'], ['c2', 'two'], ['c3', 'ccpg']]) {
    d.on('GET', `/v1/connections/${id}`, () => [200, detail(id, name)])
    d.on('GET', `/v1/catalog/${id}`, () => [
      200,
      { connection_id: id, entries: {}, unclassified: id === 'c1' ? ['public.t.a', 'public.t.b'] : [], unclassified_count: id === 'c1' ? 7 : 0 },
    ])
  }
  d.on('GET', '/v1/hosts', () => [200, [{ id: 'h1', name: 'db1', address: 'db.internal', port: 5432, sslmode: 'require', connections: ['c1', 'c2', 'c3'] }]])
  return d
}

const tabs = async () => within(await screen.findByRole('navigation', { name: 'Connection sections' }))

describe('one page per connection', () => {
  test('NAV-C4 /policy and /catalog are not found', async () => {
    daemon()
    renderApp('/policy')
    expect(await screen.findByText('Page not found')).toBeTruthy()
    renderApp('/catalog')
    expect((await screen.findAllByText('Page not found')).length).toBe(2)
  })

  test('NAV-C5 a deep link opens that tab with the database selected', async () => {
    daemon()
    renderApp('/connections/c1/settings')
    expect((await tabs()).getByRole('link', { name: 'Settings' }).getAttribute('aria-current')).toBe('page')
    expect(await screen.findByRole('region', { name: 'Reads' })).toBeTruthy()
    expect(screen.getByRole('combobox', { name: 'Connection' }).textContent).toContain('one')
  })

  test('NAV-C6 an unknown connection says so and links back', async () => {
    const d = daemon()
    d.on('GET', '/v1/connections/nope', () => [404, { code: 'unknown_connection', summary: 'no connection with that id is registered' }])
    renderApp('/connections/nope')
    expect(await screen.findByText(/no such connection/i)).toBeTruthy()
    expect(screen.getByRole('link', { name: /back to connections/i }).getAttribute('href')).toBe('/connections')
  })

  test('NAV-C7 each tab shows that connection\'s existing editor', async () => {
    daemon()
    renderApp('/connections/c1/settings')
    expect(await screen.findByRole('radio', { name: /^All columns/ })).toBeTruthy()
    await userEvent.click((await tabs()).getByRole('link', { name: 'Catalog' }))
    expect(await screen.findByText(/2 column\(s\) unclassified/)).toBeTruthy()
    await userEvent.click((await tabs()).getByRole('link', { name: 'Overview' }))
    expect(await screen.findByRole('button', { name: 'Test connection' })).toBeTruthy()
  })

  test('NAV-C8 Overview shows the counts and links each to its tab', async () => {
    daemon()
    renderApp('/connections/c1')
    const reads = await screen.findByRole('link', { name: /ask when a read is uncertain/i })
    expect(reads.getAttribute('href')).toBe('/connections/c1/settings')
    const unclassified = await screen.findByRole('link', { name: /7 unclassified columns/i })
    expect(unclassified.getAttribute('href')).toBe('/connections/c1/catalog')
  })

  test('NAV-C9 Connections rows open the connection and Settings links to Connections', async () => {
    daemon()
    renderApp('/connections')
    const main = within(await screen.findByRole('main'))
    expect((await main.findByRole('link', { name: 'one' })).getAttribute('href')).toBe('/connections/c1')
    renderApp('/settings')
    const links = await screen.findAllByRole('link', { name: 'Connections' })
    expect(links.some((l) => l.getAttribute('href') === '/connections')).toBe(true)
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
