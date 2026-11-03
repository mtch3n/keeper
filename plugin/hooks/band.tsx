import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Waiting } from '../types'

const waiting = atom({ plugin: 'keeper', key: 'waiting' } as const, null as Waiting | null)

// `keeper hook waiting` prints JSON when the daemon answers and nothing when it
// does not, and it never starts the daemon.
async function readWaiting($: EngineInterface): Promise<Waiting | null> {
  try {
    const { exitCode, stdout } = await $.process.run(['keeper', 'hook', 'waiting'])
    if (exitCode !== 0 || stdout.trim() === '') {
      return null
    }
    const reply = JSON.parse(stdout) as { waiting?: unknown; url?: unknown }
    if (typeof reply.waiting !== 'number' || reply.waiting < 1) {
      return null
    }
    return { count: reply.waiting, url: typeof reply.url === 'string' ? reply.url : '' }
  } catch {
    return null
  }
}

async function refresh($: EngineInterface): Promise<void> {
  const next = await readWaiting($)
  await update($, waiting, () => next)
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    await refresh($)
    $.clock.every(5000, () => refresh($))
    return started
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const queue = await read($, waiting)
    if (e.props.hasSurvey || queue === null) {
      return next(e)
    }

    const { Box, Link, Text } = $.ui.resolve(e)
    // A remote surface draws only an https: link and the daemon serves plain
    // http on this machine, so only the terminal can open it.
    const link = e.surface === 'terminal' && queue.url !== '' ? queue.url : null

    return (
      <Box gap={1}>
        <Text>
          keeper: {queue.count} {queue.count === 1 ? 'ticket' : 'tickets'} waiting
        </Text>
        {link === null ? null : <Link href={link} label="Open" />}
      </Box>
    )
  })
}
