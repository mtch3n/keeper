import { screen, within } from '@testing-library/react'
import { expect, test } from 'vitest'

import { fakeDaemon, renderApp } from './daemon'

test('HLT-C5 Settings says a connection is unreachable and shows no count for it', async () => {
  const d = fakeDaemon()
  const limits = { max_rows_ceiling: 1000, statement_timeout: 30000000000, scan_sample: 300 }
  d.on('GET', '/v1/doctor', () => [
    200,
    {
      version: 'test', key_source: 'test', pending_approvals: 0, open_tickets: 0, open_requests: 0, grants: 0, suspended_grants: 0,
      connections: [
        { id: 'c1', name: 'up', state: 'reachable', findings: 0, unclassified_columns: 218, catalog_fresh: true, catalog_freshness_known: true, mode: 'assisted', limits },
        { id: 'c2', name: 'down', state: 'unreachable', findings: 0, catalog_fresh: false, catalog_freshness_known: false, mode: 'assisted', limits },
      ],
    },
  ])
  renderApp('/settings')
  const down = within((await screen.findByText('down')).closest('tr') as HTMLElement)
  expect(down.getAllByText('unreachable').length).toBeGreaterThan(0)
  expect(down.queryByText('0')).toBeNull()
  const up = within(screen.getByText('up').closest('tr') as HTMLElement)
  expect(up.getByText('218')).toBeTruthy()
})
