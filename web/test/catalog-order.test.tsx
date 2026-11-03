import { screen } from '@testing-library/react'
import { expect, test } from 'vitest'

import { detail, fakeDaemon, renderApp, summary } from './daemon'

test('catalog rows are sorted by column key with unclassified first', async () => {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one')])
  d.on('GET', '/v1/catalog/c1', () => [
    200,
    {
      connection_id: 'c1',
      entries: {
        'public.products.sku': { policy: 'allow' },
        'public.legacy_records.id': { policy: 'allow' },
        'public.accounts.id': { policy: 'allow' },
      },
      unclassified: ['public.zebra.z', 'public.alpha.a'],
      unclassified_count: 2,
    },
  ])
  renderApp('/connections/c1/catalog')
  await screen.findByRole('combobox', { name: 'Policy for public.products.sku' })
  const keys = screen
    .getAllByRole('combobox')
    .map((c) => c.getAttribute('aria-label')?.replace('Policy for ', ''))
  expect(keys).toEqual([
    'public.alpha.a',
    'public.zebra.z',
    'public.accounts.id',
    'public.legacy_records.id',
    'public.products.sku',
  ])
})
