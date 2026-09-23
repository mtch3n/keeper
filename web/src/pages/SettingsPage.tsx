import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { Lamp } from '@/components/wrappers/Lamp'
import { getDoctor, type DoctorReport } from '@/lib/api'
import { age } from '@/lib/render'

/**
 * What this daemon is doing (UI.md §2.3, §2.7).
 *
 * `Policy` answers *what may this connection do*; this screen answers *what is
 * this daemon doing*, which is the question you ask when something is wrong. So
 * it reports rather than configures, and it carries `doctor`'s output rather
 * than a preferences form.
 */
export function SettingsPage() {
  const [report, setReport] = useState<DoctorReport | null>(null)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      setReport(await getDoctor())
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  if (report === null) return <Skeleton className="h-64 w-full" />

  return (
    <div className="flex flex-col gap-8">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Daemon</h2>
        <Facts>
          <Fact label="version">{report.version}</Fact>
          <Fact label="sessions">{report.sessions?.length ?? 0} connected</Fact>
          <Fact label="waiting">
            {report.pending_approvals} approval(s), {report.open_requests} local request(s)
          </Fact>
          <Fact label="tickets">{report.open_tickets} open</Fact>
          <Fact label="allow rules">
            {report.grants} · {report.suspended_grants} suspended by a schema change
          </Fact>
        </Facts>
      </section>

      <Separator />

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Vault</h2>
        {/* There is no locked state and no unlock control. keeperd opens the
            vault from the keychain, KEEPER_MASTER_KEY or key.age before it
            serves and exits if it cannot (§4.3), so a daemon this screen can
            reach has an open vault. What is left to report is which source
            answered: silent degradation to a weaker one is a defect (R4.3), so
            it is named rather than assumed. */}
        <Facts>
          <Fact label="state">
            <span className="flex items-center gap-2">
              <Lamp state="live" label="vault open" /> open
            </span>
          </Fact>
          <Fact label="key source">{report.key_source}</Fact>
        </Facts>
      </section>

      <Separator />

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Detection</h2>
        <Facts>
          {report.detector ? (
            <>
              <Fact label="detector">
                {report.detector.name} {report.detector.version ?? ''}
              </Fact>
              {/* "What was examining my data, and could it talk to anyone" has to
                  be answerable after the fact (R8.5g), so it is answerable now. */}
              <Fact label="network">
                {report.detector.network_posture === 'none' ? (
                  'no network access'
                ) : (
                  <span className="text-waiting">{report.detector.network_posture}</span>
                )}
              </Fact>
            </>
          ) : (
            <Fact label="detector">none configured — rules coverage only</Fact>
          )}
          <Fact label="judge">
            {!report.judge.configured
              ? 'not configured — uncertain output is masked rather than judged'
              : report.judge.available
                ? `${report.judge.identity ?? 'local model'}, available`
                : `${report.judge.identity ?? 'local model'}, unreachable — masked fallback in use`}
          </Fact>
        </Facts>
      </section>

      <Separator />

      <section className="flex flex-col gap-3">
        <h2 className="text-heading">Connections</h2>
        {report.connections?.length ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
                <TableHead>Name</TableHead>
                <TableHead>Mode</TableHead>
                <TableHead className="text-right">Findings</TableHead>
                <TableHead className="text-right">Unclassified</TableHead>
                <TableHead>Catalog</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {report.connections.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <Lamp state="live" />
                  </TableCell>
                  <TableCell className="text-meta">{c.name}</TableCell>
                  <TableCell className="text-meta">{c.mode}</TableCell>
                  {/* A count and a link, never a state: a finding does not stop
                      this connection, and doctor calling it a fault would be the
                      acceptance gate under another name (SPEC R4.1). */}
                  <TableCell className="text-right text-meta">
                    {c.findings > 0 ? <Link to="/audit" className="underline underline-offset-4">{c.findings}</Link> : '—'}
                  </TableCell>
                  <TableCell className="text-right text-meta">{c.unclassified_columns}</TableCell>
                  <TableCell className="text-meta">
                    {/* R5.6b: a false answer is uncertainty, not permission — and
                        so is no answer. They are shown as different things. */}
                    {!c.catalog_freshness_known ? 'freshness unknown' : c.catalog_fresh ? 'fresh' : 'stale'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-meta text-muted-foreground">no connections registered</p>
        )}
      </section>

      {report.sessions?.length ? (
        <>
          <Separator />
          <section className="flex flex-col gap-3">
            <h2 className="text-heading">Sessions</h2>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Client</TableHead>
                  <TableHead>Workspace</TableHead>
                  <TableHead>Intent</TableHead>
                  <TableHead className="text-right">Connected</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {report.sessions.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell className="text-meta">
                      {s.client.name} {s.client.version ?? ''}
                    </TableCell>
                    <TableCell className="text-meta">{s.client.workspace ?? '—'}</TableCell>
                    <TableCell className="text-sm">{s.intent ?? '—'}</TableCell>
                    <TableCell className="text-right text-meta">{age(s.connected_at)} ago</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <p className="text-meta text-muted-foreground">
              A session is one connection to the daemon. When it closes, its tickets, its tokens and its
              session-scoped grants go with it.
            </p>
          </section>
        </>
      ) : null}
    </div>
  )
}

