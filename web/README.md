# keeper — web

The `keeperd`-served UI. Vite + React 19 + TypeScript, Tailwind v4, shadcn's Base UI
variant. Read `/UI.md` and `/CONTRACT.md` §3 and §5 at the repo root before changing
anything here, and `COMPONENTS.md` before adding a component.

```
pnpm install
pnpm dev          # vite, proxies /v1 to KEEPER_DAEMON (default http://127.0.0.1:7799)
pnpm lint         # oxlint
pnpm ui-audit     # scripts/ui-audit.mjs
pnpm build        # tsc -b && vite build -> dist/, embedded by internal/api
```

Add a component with `pnpm dlx shadcn@latest add <name>` — never from memory. `src/lib/api.ts`
is the only place that calls `fetch`.
