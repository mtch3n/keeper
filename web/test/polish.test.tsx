import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, doctorReport, fakeDaemon, renderApp, summary } from './daemon'

function connections(doctor?: unknown[]) {
  const d = fakeDaemon()
  d.on('GET', '/v1/hosts', () => [
    200,
    [
      { id: 'h1', name: 'prod-db', address: 'db.internal', port: 5432, sslmode: 'require', connections: ['c1', 'c2'] },
      { id: 'h2', name: 'stage-db', address: 'stage.internal', port: 5432, sslmode: 'require', connections: ['c3'] },
    ],
  ])
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'orders'), summary('c2', 'billing'), summary('c3', 'staging')]])
  // A doctor that never answers is a fetch that never resolves.
  if (doctor) d.on('GET', '/v1/doctor', () => [200, doctorReport({ connections: doctor })])
  return d
}

describe('the Connections list', () => {
  test('POL-C3 every connection sits in one table under a row for its host', async () => {
    connections([])
    renderApp('/connections')
    await screen.findByRole('link', { name: 'orders' })
    const tables = screen.getAllByRole('table')
    expect(tables).toHaveLength(1)
    const rows = within(tables[0]).getAllByRole('row').map((r) => r.textContent ?? '')
    const at = (s: string) => rows.findIndex((r) => r.includes(s))
    expect(at('prod-db')).toBeLessThan(at('orders'))
    expect(at('orders')).toBeLessThan(at('stage-db'))
    expect(at('stage-db')).toBeLessThan(at('staging'))
  })

  test('POL-C4 a filter that matches nothing says so', async () => {
    connections([])
    renderApp('/connections')
    await screen.findByRole('link', { name: 'orders' })
    await userEvent.type(screen.getByRole('searchbox', { name: /filter/i }), 'zzz')
    expect(screen.getByText(/no connection matches/i)).toBeTruthy()
    expect(screen.queryByText('prod-db')).toBeNull()
  })
})

function editable(patch: (body: unknown) => [number, unknown] = () => [200, {}]) {
  const d = fakeDaemon()
  const sent: unknown[] = []
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one')])
  d.on('PATCH', '/v1/connections/c1', (b) => {
    sent.push(b)
    return patch(b)
  })
  return sent
}

const region = async (name: string) => within(await screen.findByRole('region', { name }))

describe('saving a connection setting', () => {
  test('POL-C7 a choice says it was saved', async () => {
    editable()
    renderApp('/connections/c1/settings')
    const reads = await region('Reads')
    await userEvent.click(reads.getByRole('radio', { name: /^Always/ }))
    expect(await reads.findByText('Saved')).toBeTruthy()
  })

  test('POL-C8 allowing writes asks first, and cancelling sends nothing', async () => {
    const sent = editable()
    renderApp('/connections/c1/settings')
    const writes = await region('Writes')
    await userEvent.click(writes.getByRole('radio', { name: /^Allow/ }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: /cancel/i }))
    expect(sent).toHaveLength(0)
    expect(writes.getByRole('radio', { name: /^Off/ }).getAttribute('aria-checked')).toBe('true')
  })

  test('POL-C9 a row ceiling that is not a positive whole number cannot be saved', async () => {
    const sent = editable()
    renderApp('/connections/c1/settings')
    const limits = await region('Limits')
    const ceiling = limits.getByLabelText(/row ceiling/i)
    for (const bad of ['abc', '0']) {
      await userEvent.clear(ceiling)
      await userEvent.type(ceiling, bad)
      expect(limits.getByRole('button', { name: 'Save' }).hasAttribute('disabled')).toBe(true)
      expect(limits.getByText(/whole number above 0/i)).toBeTruthy()
    }
    expect(sent).toHaveLength(0)
  })

  test('POL-C11 a refused save says so in its own section', async () => {
    editable(() => [400, { code: 'syntax', summary: 'reads must be always, uncertain or never' }])
    renderApp('/connections/c1/settings')
    const reads = await region('Reads')
    await userEvent.click(reads.getByRole('radio', { name: /^Always/ }))
    expect((await reads.findByRole('alert')).textContent).toMatch(/reads must be always, uncertain or never/)
  })
})

describe('reading a settings page', () => {
  test('POL-C12 a choice\'s consequence shows and its section\'s reasoning waits behind Why', async () => {
    editable()
    renderApp('/connections/c1/settings')
    const reads = await region('Reads')
    expect(reads.getByText(/ask you before every read/i)).toBeTruthy()
    expect(reads.queryByText(/each runs in a read-only transaction/i)).toBeNull()
    await userEvent.click(reads.getByRole('button', { name: 'Why' }))
    expect(await reads.findByText(/each runs in a read-only transaction/i)).toBeTruthy()
  })
})

describe('the accessibility floor', () => {
  test('POL-C13 Inbox and Activity each have one level-one heading', async () => {
    for (const path of ['/inbox', '/activity']) {
      const d = fakeDaemon()
      d.on('GET', '/v1/approvals', () => [200, []])
      const { unmount } = renderApp(path)
      await waitFor(() => expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1))
      unmount()
    }
  })

  test('POL-C14 every lamp reaches 3:1 against the page in both themes', async () => {
    const { readFileSync } = await import('node:fs')
    const css = readFileSync(`${process.cwd()}/src/index.css`, 'utf8')
    const block = (sel: string) => css.slice(css.indexOf(sel + ' {'), css.indexOf('\n}', css.indexOf(sel + ' {')))
    const token = (b: string, name: string) => {
      const m = new RegExp(`--${name}: oklch\\(([\\d.]+) ([\\d.]+) ([\\d.]+)`).exec(b)
      if (!m) throw new Error(`no --${name}`)
      return m.slice(1, 4).map(Number) as [number, number, number]
    }
    // OKLCH -> linear sRGB -> relative luminance (WCAG 2).
    const luminance = ([L, C, h]: [number, number, number]) => {
      const a = C * Math.cos((h * Math.PI) / 180)
      const b = C * Math.sin((h * Math.PI) / 180)
      const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
      const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
      const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
      const rgb = [
        4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
        -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
        -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
      ].map((v) => Math.min(1, Math.max(0, v)))
      return 0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]
    }
    const contrast = (x: number, y: number) => (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
    for (const theme of [':root', '.dark']) {
      const b = block(theme)
      const bg = luminance(token(b, 'background'))
      for (const lamp of ['live', 'waiting', 'blocked']) {
        expect(contrast(luminance(token(b, lamp)), bg), `${theme} ${lamp}`).toBeGreaterThanOrEqual(3)
      }
    }
  })
})
