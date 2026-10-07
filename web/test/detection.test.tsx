import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, fakeDaemon, renderApp, summary } from './daemon'

function pipeline() {
  const d = fakeDaemon()
  const sent: unknown[] = []
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', '2026-10-06T12:00:00Z', [])])
  d.on('PATCH', '/v1/connections/c1', (b) => {
    sent.push(b)
    return [200, {}]
  })
  return sent
}

const detection = async () => within(await screen.findByRole('region', { name: 'Detection' }))

describe('the detection pipeline', () => {
  test('a stage is added, ordered and filtered in a draft, and saved whole', async () => {
    const sent = pipeline()
    renderApp('/connections/c1/detection')
    const section = await detection()
    expect(section.getByRole('button', { name: 'Save' }).hasAttribute('disabled')).toBe(true)

    await userEvent.click(section.getByRole('combobox', { name: 'Add stage' }))
    await userEvent.click(await screen.findByRole('option', { name: 'Your list' }))
    await userEvent.click(section.getAllByRole('button', { name: 'Move up' })[1])
    await userEvent.type(section.getByLabelText('Only these entities', { selector: '#entities-patterns' }), 'email_address')
    expect(sent).toHaveLength(0)

    await userEvent.click(section.getByRole('button', { name: 'Save' }))
    expect(await section.findByText('Saved')).toBeTruthy()
    expect(sent).toEqual([{ detection: [{ kind: 'list' }, { kind: 'patterns', entities: ['email_address'] }] }])
  })

  test('removing every stage says scan cells are redacted whole, and Reset restores the saved pipeline', async () => {
    pipeline()
    renderApp('/connections/c1/detection')
    const section = await detection()
    await userEvent.click(section.getByRole('button', { name: 'Remove Patterns' }))
    expect(section.getByText(/redacted whole/)).toBeTruthy()
    await userEvent.click(section.getByRole('button', { name: 'Reset' }))
    expect(section.getByRole('button', { name: 'Remove Patterns' })).toBeTruthy()
  })
})
