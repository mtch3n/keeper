/** The tabs of a connection's page, in the order they read. */
export const TABS = [
  { tab: 'overview', label: 'Overview' },
  { tab: 'detection', label: 'Detection' },
  { tab: 'catalog', label: 'Catalog' },
  { tab: 'limits', label: 'Limits' },
  { tab: 'privileges', label: 'Privileges' },
] as const

export type Tab = (typeof TABS)[number]['tab']

/** The path of one tab of one connection; Overview is the bare page. */
export function tabPath(id: string, tab: Tab): string {
  return tab === 'overview' ? `/connections/${id}` : `/connections/${id}/${tab}`
}
