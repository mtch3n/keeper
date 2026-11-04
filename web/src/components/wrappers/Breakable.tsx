import { Fragment } from 'react'

/**
 * A dotted or underscored name that wraps only after a `.` or `_`, never in
 * the middle of a word. A cell holding `public.customers.street` can then
 * narrow to one segment per line and stay readable.
 */
export function Breakable({ text }: { text: string }) {
  return (
    <>
      {text.split(/(?<=[._])/).map((part, i) => (
        <Fragment key={i}>
          {i > 0 ? <wbr /> : null}
          {part}
        </Fragment>
      ))}
    </>
  )
}
