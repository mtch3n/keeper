/**
 * The `(!)` beside a connection running with accepted G0 findings (SPEC
 * R4.1). It is the only amber on a screen other than the `waiting` lamp and
 * it carries the same meaning: this needs you to know something.
 *
 * It lives here rather than beside the one screen that first needed it
 * because `Approvals` and `AppSidebar` both mark the same fact, and
 * COMPONENTS.md policy 6 is that two screens showing the same thing show it
 * with the same component. The switcher this replaced drew a bare warning
 * glyph of its own, which is a second rendering of one meaning and drifts
 * the moment either is touched.
 */
export function AcceptedFindingsMark({ className }: { className?: string }) {
  return (
    <span
      className={className ?? 'ml-2 text-waiting'}
      title="This connection runs with accepted privilege findings: keeper's database-level protection does not apply to it."
    >
      (!)
    </span>
  )
}
