import { screen, within } from '@testing-library/react'
import { expect, test } from 'vitest'

import { detail, fakeDaemon, renderApp, summary } from './daemon'

test('JDG-C8 the modes are strict and assisted, and neither mentions a model', async () => {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', '2026-10-06T12:00:00Z', [])])
  renderApp('/policy')
  const heading = await screen.findByRole('heading', { name: 'Mode' })
  const section = within(heading.closest('section') as HTMLElement)
  const radios = section.getAllByRole('radio')
  expect(radios).toHaveLength(2)
  expect(section.getByText(/strict/i)).toBeTruthy()
  expect(section.getByText(/assisted/i)).toBeTruthy()
  expect(section.queryByText(/permissive/i)).toBeNull()
  expect(heading.closest('section')?.textContent ?? '').not.toMatch(/model/i)
})
