import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, fakeDaemon, NEVER, renderApp, summary } from './daemon'

function catalogOf(unclassified: string[], entries: Record<string, unknown> = {}) {
  const d = fakeDaemon()
  const saved: unknown[] = []
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', NEVER, [])])
  d.on('GET', '/v1/catalog/c1', () => [
    200,
    { connection_id: 'c1', entries, unclassified, unclassified_count: unclassified.length },
  ])
  d.on('PUT', '/v1/catalog/c1/columns', (body) => {
    saved.push(body)
    return [200, {}]
  })
  return { d, saved }
}

async function choosePolicy(column: string, policy: string) {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('combobox', { name: `Policy for ${column}` }))
  await user.click(await screen.findByRole('option', { name: policy }))
  return user
}

describe('declaring a PII column', () => {
  test('MASK-C15 choosing token on an unclassified column saves one valid entry', async () => {
    const { saved } = catalogOf(['public.users.email'])
    renderApp('/connections/c1/catalog')
    await choosePolicy('public.users.email', 'token')
    await waitFor(() => expect(saved.length).toBe(1))
    expect(saved[0]).toMatchObject({
      entries: { 'public.users.email': { policy: 'token', namespace: 'email' } },
    })
  })

  test('MASK-C16 an empty namespace does not save and the field says why', async () => {
    const { saved } = catalogOf([], { 'public.users.email': { policy: 'token', namespace: 'email' } })
    renderApp('/connections/c1/catalog')
    const user = userEvent.setup()
    const field = await screen.findByRole('textbox', { name: 'Namespace for public.users.email' })
    await user.clear(field)
    await user.tab()
    expect(await screen.findByText(/needs a namespace/i)).toBeTruthy()
    expect(saved.length).toBe(0)
  })
})
