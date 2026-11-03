/** The tabs of a connection's page, in the order they read. */
export const TABS = [
  { tab: 'overview', label: 'Overview' },
  { tab: 'settings', label: 'Settings' },
  { tab: 'catalog', label: 'Catalog' },
] as const

export type Tab = (typeof TABS)[number]['tab']

/** The path of one tab of one connection; Overview is the bare page. */
export function tabPath(id: string, tab: Tab): string {
  return tab === 'overview' ? `/connections/${id}` : `/connections/${id}/${tab}`
}
