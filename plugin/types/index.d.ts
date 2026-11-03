export type Waiting = { count: number; url: string }

declare module 'claude-code' {
  interface PluginState {
    keeper: { waiting: Waiting | null }
  }
}
