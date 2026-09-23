import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { auditConnection, listAudits, subscribeToEvents } from '@/lib/api'
import { auditedAge } from '@/lib/render'
import type { AuditReport, Finding } from '@/lib/types'

/**
 * The privilege audit, on its own (SPEC R4.1, UI.md §2.7).
 *
 * It used to live at the end of registration, where every finding was a
 * checkbox standing between the operator and a connection they were trying to
 * set up. That framing was wrong twice over. It made the audit read as
 * paperwork to clear rather than as a report, and it put the decision at the
 * one moment the reader has least attention for it — mid-setup, wanting the
 * thing to work.
 *
 * So the audit is a screen you visit, and a connection is usable whether or
 * not you ever visit it. What a finding earns here is a suggested fix: the
 * statement that would narrow the grant, copyable as-is. keeper does not run
 * it. It holds a credential whose privileges are the subject of the report,
 * and a tool that can narrow its own grants is a tool that can widen them.
 */
export function AuditPage() {
  const [reports, setReports] = useState<AuditReport[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      setReports(await listAudits())
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    void refresh()
    return subscribeToEvents((event) => {
      if (event.kind === 'connection') void refresh()
    })
  }, [refresh])

  const rerun = async (id: string) => {
    setBusy(id)
    try {
      await auditConnection(id)
      await refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  if (reports === null) return <Skeleton className="h-40 w-full" />

  if (reports.length === 0) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Nothing to audit</EmptyTitle>
          <EmptyDescription>
            Register a connection and keeper audits its role, then reports here what that role can do beyond
            reading.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const total = reports.reduce((n, r) => n + (r.findings?.length ?? 0), 0)

  return (
    <div className="flex flex-col gap-8">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <div>
        <h1 className="text-title">Audit</h1>
        <p className="text-sm text-muted-foreground">
          What each registered role can do beyond reading, and the statement that would narrow it. None of it
          stops a connection from working — these are changes to make on the database, and keeper does not make
          them for you.
        </p>
      </div>

      {total === 0 ? (
        <p className="text-sm">
          Every registered role is clean: none of them holds a privilege keeper would report.
        </p>
      ) : null}

      {reports.map((report) => (
        <ConnectionAudit
          key={report.connection_id}
          report={report}
          busy={busy === report.connection_id}
          onRerun={() => rerun(report.connection_id)}
        />
      ))}
    </div>
  )
}

function ConnectionAudit({
  report,
  busy,
  onRerun,
}: {
  report: AuditReport
  busy: boolean
  onRerun: () => void
}) {
  const findings = report.findings ?? []

  return (
    <div className="flex flex-col gap-5 border border-border p-6">
      <div className="flex items-start justify-between gap-4">
        <Facts>
          <Fact label="connection">{report.name}</Fact>
          <Fact label="role">{report.role}</Fact>
          <Fact label="database">{report.database}</Fact>
          <Fact label="audited">{auditedAge(report.audited_at)}</Fact>
        </Facts>
        <Button variant="outline" disabled={busy} onClick={onRerun}>
          {busy ? 'Re-auditing…' : 'Re-audit'}
        </Button>
      </div>

      <Separator />

      {findings.length === 0 ? (
        <p className="text-sm">
          Nothing to report: this role holds no privilege keeper would flag.
        </p>
      ) : (
        <div className="flex flex-col gap-5">
          {findings.map((f) => (
            <FindingRow key={f.id} finding={f} />
          ))}
        </div>
      )}
    </div>
  )
}

/** One finding: what it is, what it means in a sentence, and the fix. The fix
 * is the whole reason this screen is worth opening, so it is shown in full
 * rather than behind a disclosure — a statement you have to click to see is
 * one you will not paste. */
function FindingRow({ finding }: { finding: Finding }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <div className="flex items-baseline gap-3">
        <span className="text-meta">{finding.id}</span>
        <span className="text-label text-muted-foreground">{finding.kind}</span>
      </div>
      <span className="text-sm">{finding.detail}</span>
      {finding.narrower ? (
        <>
          <span className="text-label text-muted-foreground">suggested fix</span>
          <pre className="overflow-x-auto text-meta whitespace-pre-wrap">{finding.narrower}</pre>
        </>
      ) : (
        <span className="text-meta text-muted-foreground">
          No single statement removes this one — it may be a vendor-owned object you cannot revoke on.
        </span>
      )}
    </div>
  )
}
