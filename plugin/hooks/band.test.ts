import type { On, RenderSurface } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'

const PROMPT = {
  hasSurvey: false,
  isWorking: false,
  maxRows: 10,
  bodyColumns: 100,
  scroll: { offset: 0, bodyRows: 10 },
  view: {},
}

const URL = 'http://127.0.0.1:7773/?t=x'

type World = { waiting: number; isServing: boolean }

function exited(exitCode: number, stdout: string) {
  return { exitCode, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
}

// Stands for the engine beneath the plugin, with `keeper hook waiting` answered
// as the CLI does: JSON when the daemon answers, nothing when it does not.
function fakeKeeper(on: On, world: World) {
  const ran: string[][] = []
  const clock = mock.clock(on)
  on('session.start', (_$, e) => ({ cwd: e.cwd }))
  on('ui.render', { component: 'AbovePrompt' }, ($, e) => $.ui.resolve(e).Box({ key: 'engine' }))
  on('process.run', (_$, e) => {
    ran.push([...e.argv])
    if (e.argv.join(' ') !== 'keeper hook waiting') {
      return { deny: `unexpected ${e.argv.join(' ')}` }
    }
    return { value: exited(0, world.isServing ? JSON.stringify({ waiting: world.waiting, url: URL }) : '') }
  })
  return { ran, clock }
}

function band($: Engine, surface: RenderSurface) {
  return $.ui.mount({ plugin: 'keeper', surface, component: 'AbovePrompt', props: PROMPT })
}

function start($: Engine, surface: RenderSurface) {
  return $.session.start({ cwd: '/work/repo', surface, isInteractive: true })
}

test('PLG-C13 a waiting ticket shows its count and the link, no button, and the band clears when decided', async ($, on) => {
  const world: World = { waiting: 2, isServing: true }
  const { clock } = fakeKeeper(on, world)
  await start($, 'terminal')
  const drawn = await band($, 'terminal')
  expect((await drawn.find({ type: 'Text' }))?.text).toBe('keeper: 2 tickets waiting')
  expect((await drawn.find({ type: 'Link' }))?.props.href).toBe(URL)
  expect(await drawn.find({ type: 'Button' })).toBeUndefined()

  world.waiting = 1
  await clock.advance(5000)
  expect((await drawn.find({ type: 'Text' }))?.text).toBe('keeper: 1 ticket waiting')

  world.waiting = 0
  await clock.advance(5000)
  expect(await drawn.find({ type: 'Text' })).toBeUndefined()
  expect(await drawn.find({ type: 'Link' })).toBeUndefined()
})

test('PLG-C15 a daemon that is down draws no band and nothing but the read runs', async ($, on) => {
  const world: World = { waiting: 3, isServing: false }
  const { ran } = fakeKeeper(on, world)
  for (const surface of ['terminal', 'desktop'] as const) {
    await start($, surface)
    const drawn = await band($, surface)
    expect(await drawn.find({ key: 'engine' })).toBeDefined()
    expect(await drawn.find({ type: 'Text' })).toBeUndefined()
  }
  expect(ran.every(argv => argv.join(' ') === 'keeper hook waiting')).toBe(true)
})
