import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test } from 'vitest'

import { detail, doctorReport, fakeDaemon, finding, renderApp, summary } from './daemon'

const limits = { max_rows_ceiling: 1000, max_bytes: 1048576, statement_timeout: 30000000000, scan_sample: 300 }

function health(id: string, name: string, state: string, extra: Record<string, unknown> = {}) {
  return { id, name, state, findings: 0, catalog_fresh: true, catalog_freshness_known: true, mode: 'assisted', limits, ...extra }
}

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

const row = async (name: string) => (await screen.findByRole('link', { name })).closest('tr') as HTMLElement

describe('the Connections list', () => {
  test('POL-C1 a lamp shows whether its connection is reachable', async () => {
    connections([health('c1', 'orders', 'reachable'), health('c2', 'billing', 'unreachable'), health('c3', 'staging', 'reachable')])
    renderApp('/connections')
    await waitFor(async () => expect(within(await row('orders')).getByRole('img').getAttribute('data-state')).toBe('live'))
    const down = within(await row('billing')).getByRole('img')
    expect(down.getAttribute('data-state')).toBe('blocked')
    expect(down.getAttribute('aria-label')).toMatch(/unreachable/i)
  })

  test('POL-C2 a connection nobody has checked is neither green nor amber', async () => {
    connections([health('c1', 'orders', 'unknown')])
    renderApp('/connections')
    for (const name of ['orders', 'billing']) {
      const lamp = within(await row(name)).getByRole('img')
      expect(lamp.getAttribute('data-state')).toBe('idle')
    }
  })

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

function audited(findings: ReturnType<typeof finding>[]) {
  const d = fakeDaemon()
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', '2026-10-06T12:00:00Z', findings)])
  return d
}

const writeOn = (t: string) =>
  finding(`relation-write:public.${t}`, `This role can change rows in public.${t}.`, `REVOKE INSERT, UPDATE, DELETE ON public.${t} FROM app_ro;`)

describe('the Privileges tab', () => {
  test('POL-C5 findings are grouped by kind with one statement per group and one script for all', async () => {
    const user = userEvent.setup()
    const schema = { ...finding('schema-create:public', 'This role can create objects in schema public.', 'REVOKE CREATE ON SCHEMA public FROM app_ro;'), kind: 'schema-create' }
    audited([writeOn('orders'), writeOn('users'), writeOn('items'), schema])
    renderApp('/connections/c1/privileges')
    const p = within(await screen.findByRole('region', { name: 'Privileges' }))
    const groups = p.getAllByRole('heading', { level: 3 }).map((h) => h.textContent ?? '')
    expect(groups[0]).toMatch(/schema-create/)
    expect(groups[1]).toMatch(/relation-write.*3/)
    const writes = p.getByLabelText('relation-write statements')
    for (const t of ['orders', 'users', 'items']) expect(writes.textContent).toContain(`public.${t}`)

    await user.click(p.getByRole('button', { name: /copy all fixes/i }))
    const script = await navigator.clipboard.readText()
    expect(script.trim().startsWith('BEGIN;')).toBe(true)
    expect(script.trim().endsWith('COMMIT;')).toBe(true)
    for (const s of ['public.orders', 'public.users', 'public.items', 'CREATE ON SCHEMA public']) expect(script).toContain(s)
  })

  test('POL-C6 a finding no statement removes is set apart and left out of the script', async () => {
    const user = userEvent.setup()
    const vendor = { ...finding('security-definer:pg_catalog.azure_fn', 'A vendor function runs with its owner\'s rights.', ''), kind: 'security-definer', narrower: undefined }
    audited([writeOn('orders'), vendor])
    renderApp('/connections/c1/privileges')
    const p = within(await screen.findByRole('region', { name: 'Privileges' }))
    expect(p.getByRole('heading', { level: 3, name: /no single statement removes/i })).toBeTruthy()
    await user.click(p.getByRole('button', { name: /copy all fixes/i }))
    const script = await navigator.clipboard.readText()
    expect(script).toContain('public.orders')
    expect(script).not.toContain('azure_fn')
  })
})

function editable(patch: (body: unknown) => [number, unknown] = () => [200, {}]) {
  const d = fakeDaemon()
  const sent: unknown[] = []
  d.on('GET', '/v1/connections', () => [200, [summary('c1', 'one')]])
  d.on('GET', '/v1/connections/c1', () => [200, detail('c1', 'one', '2026-10-06T12:00:00Z', [])])
  d.on('PATCH', '/v1/connections/c1', (b) => {
    sent.push(b)
    return patch(b)
  })
  d.on('PUT', '/v1/connections/c1/terms', (b) => {
    sent.push(b)
    return [200, { deny: 0, allow: 0, patterns: 0 }]
  })
  return sent
}

const region = async (name: string) => within(await screen.findByRole('region', { name }))

describe('saving a connection setting', () => {
  test('POL-C7 a choice says it was saved', async () => {
    editable()
    renderApp('/connections/c1/limits')
    const mode = await region('Mode')
    await userEvent.click(mode.getByRole('radio', { name: /strict/i }))
    expect(await mode.findByText('Saved')).toBeTruthy()
  })

  test('POL-C8 widening writes asks first, and cancelling sends nothing', async () => {
    const sent = editable()
    renderApp('/connections/c1/limits')
    const writes = await region('Writes')
    await userEvent.click(writes.getByRole('radio', { name: /writes with approval/i }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: /cancel/i }))
    expect(sent).toHaveLength(0)
    expect(writes.getByRole('radio', { name: /read-only/i }).getAttribute('aria-checked')).toBe('true')
  })

  test('POL-C9 a row ceiling that is not a positive whole number cannot be saved', async () => {
    const sent = editable()
    renderApp('/connections/c1/limits')
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

  test('POL-C10 empty terms cannot be saved, and clearing asks first', async () => {
    const sent = editable()
    renderApp('/connections/c1/detection')
    const terms = await region('Your terms')
    expect(terms.getByRole('button', { name: 'Save terms' }).hasAttribute('disabled')).toBe(true)
    await userEvent.click(terms.getByRole('button', { name: /clear all terms/i }))
    const dialog = await screen.findByRole('alertdialog')
    expect(sent).toHaveLength(0)
    await userEvent.click(within(dialog).getByRole('button', { name: /clear all terms/i }))
    await waitFor(() => expect(sent).toHaveLength(1))
  })

  test('POL-C11 a refused save says so in its own section', async () => {
    editable(() => [400, { code: 'syntax', summary: 'mode must be strict or assisted' }])
    renderApp('/connections/c1/limits')
    const mode = await region('Mode')
    await userEvent.click(mode.getByRole('radio', { name: /strict/i }))
    expect((await mode.findByRole('alert')).textContent).toMatch(/mode must be strict or assisted/)
  })
})

describe('reading a settings page', () => {
  test('POL-C12 a choice\'s consequence shows and its section\'s reasoning waits behind Why', async () => {
    editable()
    renderApp('/connections/c1/limits')
    const mode = await region('Mode')
    expect(mode.getByText(/known-safe reads run/i)).toBeTruthy()
    const deny = await region('Denylist')
    expect(deny.queryByText(/evaluated against the plan/i)).toBeNull()
    await userEvent.click(deny.getByRole('button', { name: 'Why' }))
    expect(await deny.findByText(/evaluated against the plan/i)).toBeTruthy()
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
