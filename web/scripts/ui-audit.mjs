#!/usr/bin/env node

/**
 * UI Audit — enforces UI.md §1, §3 and CONTRACT.md §5 mechanically. Modelled
 * on trellis's `web/scripts/ui-audit.js`; trellis wins on mechanics
 * (CONTRACT.md §5), keeper's own additions are called out below.
 *
 * `src/components/ui/` is the shadcn registry: its own source legitimately
 * contains raw HTML primitives, arbitrary variant selectors and radius
 * utilities that resolve to 0 through the theme tokens. Every check below
 * audits consumers of the registry, not the registry itself, exactly as
 * trellis's script does.
 */

import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.join(__dirname, '..', 'src')

const issues = []

function getAllTsxFiles(dir) {
  const files = []
  function walk(current) {
    let entries
    try {
      entries = fs.readdirSync(current)
    } catch {
      return
    }
    for (const entry of entries) {
      const fullPath = path.join(current, entry)
      let stat
      try {
        stat = fs.statSync(fullPath)
      } catch {
        continue
      }
      if (stat.isDirectory()) {
        if (entry !== 'node_modules' && entry !== 'dist') walk(fullPath)
      } else if (entry.endsWith('.tsx') || entry.endsWith('.ts')) {
        files.push(fullPath)
      }
    }
  }
  walk(dir)
  return files
}

function getLineNumber(content, index) {
  return content.substring(0, index).split('\n').length
}

function isRegistryFile(file) {
  return file.includes(`${path.sep}components${path.sep}ui${path.sep}`)
}

function report(file, line, message) {
  issues.push({ file, line, message })
}

// ── Check 1: imports only from allowed sources ──────────────────────────────
function checkImports(files) {
  const allowedSources = [
    '@/components/ui',
    '@/components/wrappers',
    '@/lib',
    '@/pages',
    'react',
    'react-dom',
    'react-router-dom',
    'lucide-react',
  ]

  for (const file of files) {
    if (isRegistryFile(file)) continue
    if (file.includes(`${path.sep}components${path.sep}wrappers${path.sep}`)) continue // the composition layer

    const content = fs.readFileSync(file, 'utf-8')
    const importRegex = /import\s+(?:type\s+)?\{([^}]+)\}\s+from\s+['"]([^'"]+)['"]/g
    let match
    while ((match = importRegex.exec(content)) !== null) {
      const [, imports, source] = match
      const isRelative = source.startsWith('.')
      const isAllowed = isRelative || allowedSources.some((a) => source === a || source.startsWith(a + '/'))
      if (isAllowed) continue

      const hasComponentImport = imports
        .split(',')
        .map((s) => s.split(' as ')[0].trim())
        .some((name) => /^[A-Z]/.test(name))

      if (hasComponentImport) {
        report(
          file,
          getLineNumber(content, match.index),
          `Component imported from "${source}". Only @/components/ui, @/components/wrappers, @/lib, react, react-dom, react-router-dom, lucide-react and relative imports are allowed.`,
        )
      }
    }
  }
}

// ── Check 2: raw HTML primitives where a registry component exists ─────────
function checkRawPrimitives(files) {
  const primitives = ['button', 'input', 'select', 'textarea', 'dialog', 'table']
  const primitiveRegex = new RegExp(`<(${primitives.join('|')})([\\s>])`, 'g')

  for (const file of files) {
    if (isRegistryFile(file)) continue
    const content = fs.readFileSync(file, 'utf-8')
    let match
    while ((match = primitiveRegex.exec(content)) !== null) {
      report(
        file,
        getLineNumber(content, match.index),
        `Raw <${match[1]}> element. Use the shadcn component instead (Button, Input, Select, Textarea, Dialog, Table).`,
      )
    }
  }
}

// ── Check 3: every custom component (declared, exported or not) is documented ─
function checkDocumentation(files) {
  const componentsFile = path.join(__dirname, '..', 'COMPONENTS.md')
  if (!fs.existsSync(componentsFile)) {
    report(undefined, undefined, 'web/COMPONENTS.md not found. Required for the component registry (UI.md §1).')
    return
  }
  const componentsContent = fs.readFileSync(componentsFile, 'utf-8')
  const documented = new Set()
  const documentedRegex = /^\|\s*([A-Z][a-zA-Z0-9]*)\s*\|/gm
  let match
  while ((match = documentedRegex.exec(componentsContent)) !== null) documented.add(match[1])

  for (const file of files) {
    if (isRegistryFile(file)) continue
    const content = fs.readFileSync(file, 'utf-8')
    const exportRegex = /(?:export\s+)?(?:function|const)\s+([A-Z][a-zA-Z0-9]*)\s*[=(]/g
    let m
    while ((m = exportRegex.exec(content)) !== null) {
      const name = m[1]
      if (!/[a-z]/.test(name)) continue // SCREAMING_CASE is data, not a component
      const initialiser = content.slice(m.index, m.index + 160)
      if (/=\s*(createContext|lazy)\b/.test(initialiser)) continue
      if (!documented.has(name)) {
        report(file, getLineNumber(content, m.index), `Component "${name}" is not documented in web/COMPONENTS.md.`)
      }
    }
  }
}

// ── Check 4: ad-hoc colour and arbitrary values ─────────────────────────────
const PALETTE =
  'slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose'

function checkAdHocValues(files) {
  for (const file of files) {
    if (isRegistryFile(file)) continue
    const content = fs.readFileSync(file, 'utf-8')

    // Hex colours, anywhere.
    for (const m of content.matchAll(/#[0-9a-fA-F]{3,8}\b/g)) {
      report(file, getLineNumber(content, m.index), `Hex colour "${m[0]}". Use a theme token.`)
    }
    // rgb()/rgba()/hsl() literals.
    for (const m of content.matchAll(/\b(?:rgba?|hsla?)\(/g)) {
      report(file, getLineNumber(content, m.index), `Ad-hoc colour function "${m[0]}". Use a theme token (oklch lives in index.css).`)
    }
    // Raw Tailwind palette colours bypass the theme, keeper's four semantics included.
    for (const m of content.matchAll(new RegExp(`\\b(?:text|bg|border|fill|stroke|ring|decoration|outline)-(?:${PALETTE})-\\d{2,3}\\b`, 'g'))) {
      report(file, getLineNumber(content, m.index), `Raw palette colour "${m[0]}". Use a semantic token or Lamp's state colours.`)
    }
    // Arbitrary values in square brackets — p-[13px], text-[15px], w-[240px]…
    for (const m of content.matchAll(/\b[a-z-]+-\[[^\]]+\]/g)) {
      const token = m[0]
      // Arbitrary *variants* (aria-[current=page]:, data-[state=open]:, [&_svg]:)
      // select an element and carry no colour, spacing or radius.
      if (content[m.index + token.length] === ':') continue
      if (/^(?:aria|data)-\[/.test(token)) continue
      // Viewport-relative lengths have no token equivalent in the scale.
      if (/^\w+-\[\d+(?:\.\d+)?(?:vh|vw|svh|dvh|lvh|svw|dvw)\]$/.test(token)) continue
      report(file, getLineNumber(content, m.index), `Arbitrary Tailwind value "${token}". Use a theme token from the scale.`)
    }
  }
}

// ── Check 5: banned shapes and effects (UI.md §3) ───────────────────────────
function checkBannedPatterns(files) {
  const RULES = [
    [/\brounded-(?:xl|2xl|3xl|4xl|full)\b/g, '--radius is 0rem: every screen is square. Use rounded-sm (or omit it; the default already resolves to 0).'],
    [/\bshadow-(?:lg|xl|2xl)\b/g, 'Arbitrary Tailwind shadow. Use --shade / --shade-strong / --shade-deep through a component, not a raw shadow utility.'],
    [/\bbackdrop-blur(?:-\w+)?\b/g, 'Glassmorphism. Ad-hoc and illegible over data (UI.md §3.2).'],
    [/\bborder-l-(?:[2-9]|1[0-6])\b/g, 'The eyelash: a coloured left-border accent stripe (UI.md §3.1). State is Lamp\'s job.'],
    [/\bbg-gradient-to-\w+\b/g, 'Gradient. Colour without meaning (UI.md §3.2).'],
    [new RegExp(`\\b(?:from|via|to)-(?:${PALETTE})-\\d{2,3}\\b`, 'g'), 'Gradient colour stop. Colour without meaning (UI.md §3.2).'],
    [/\bhover:scale-\d+\b/g, 'Bouncy hover scale. Motion here is functional only (UI.md §3.2).'],
    [/\bspace-[xy]-\d+\b/g, 'space-x-*/space-y-* is banned. Use flex with gap-*.'],
    [/\basChild\b/g, 'asChild is Radix-only. This project is the Base UI variant — use the render prop instead.'],
  ]

  for (const file of files) {
    if (isRegistryFile(file)) continue
    // Lamp's live state is the one documented exception to the radius rule:
    // a pilot lamp, not a work state (trellis COMPONENTS.md "Shape"; recorded
    // for Lamp below in this repo's own COMPONENTS.md).
    const isLamp = file.endsWith(`${path.sep}Lamp.tsx`)
    const content = fs.readFileSync(file, 'utf-8')
    for (const [re, why] of RULES) {
      for (const m of content.matchAll(re)) {
        if (isLamp && re.source.startsWith('\\brounded-') && m[0] === 'rounded-full') continue
        report(file, getLineNumber(content, m.index), `"${m[0]}" — ${why}`)
      }
    }
  }
}

// ── Check 6: one type scale, sentence case, no eyebrows ─────────────────────
function checkTypeScale(files) {
  const RULES = [
    [/(?<![\w-])text-(?:base|lg|xl|[2-9]xl)\b/g, 'is off the type scale. Use text-title, text-heading, text-body, text-sm, text-xs, text-label or text-meta.'],
    [/(?<![\w-])(?:uppercase|lowercase|capitalize)\b/g, 'sets case by hand. The interface is sentence case throughout.'],
    [/(?<![\w-])tracking-[\w-]+/g, 'tracks text by hand. Nothing is tracked out.'],
    [/(?<![\w-])font-mono\b/g, 'sets mono by hand. Use text-meta, which carries the mono family and size together.'],
    [/<Empty\b[^>]*className="[^"]*(?<![\w-])(?:border(?:-[lrtbxy])?|bg-card)(?![\w-])/g, 'boxes an empty state. An empty state is words and, when there is one, an action — never a card.'],
  ]

  for (const file of files) {
    const content = fs.readFileSync(file, 'utf-8')
    for (const m of content.matchAll(/from\s+['"]cn['"]/g)) {
      report(file, getLineNumber(content, m.index), 'Import cn from @/lib/utils. The bare "cn" package does not know the type scale and drops custom sizes when merging.')
    }
    if (isRegistryFile(file)) continue
    for (const [re, why] of RULES) {
      for (const m of content.matchAll(re)) {
        const lineStart = content.lastIndexOf('\n', m.index) + 1
        const line = content.slice(lineStart, content.indexOf('\n', m.index) === -1 ? undefined : content.indexOf('\n', m.index))
        if (/^\s*(\/\/|\*|\/\*)/.test(line)) continue
        report(file, getLineNumber(content, m.index), `"${m[0]}" ${why}`)
      }
    }
  }
}

// ── Check 7: emoji in JSX text ───────────────────────────────────────────────
// Deliberately narrow: UI.md itself prescribes a small, meaningful glyph set
// (·, —, █, ▒, and ⚠ for an accepted-findings marker, UI.md §2.5). Only the
// decorative pictograph ranges AI slop actually uses are flagged.
const EMOJI_RANGE =
  /[\u{1F300}-\u{1FAFF}\u{1F1E6}-\u{1F1FF}✂-➰Ⓜ⤴⤵〰〽㊗㊙]/gu

function checkEmoji(files) {
  for (const file of files) {
    if (isRegistryFile(file)) continue
    const content = fs.readFileSync(file, 'utf-8')
    for (const m of content.matchAll(EMOJI_RANGE)) {
      report(file, getLineNumber(content, m.index), `Emoji "${m[0]}" in source. UI.md §3.2 bans emoji in headings, labels and empty states.`)
    }
  }
}

// ── run ──────────────────────────────────────────────────────────────────────
const files = getAllTsxFiles(SRC)
checkImports(files)
checkRawPrimitives(files)
checkDocumentation(files)
checkAdHocValues(files)
checkBannedPatterns(files)
checkTypeScale(files)
checkEmoji(files)

if (issues.length > 0) {
  console.error('\nUI Audit found issues:\n')
  for (const issue of issues) {
    const location = issue.file ? `${path.relative(process.cwd(), issue.file)}:${issue.line}` : 'global'
    console.error(`[ERROR] ${location}\n  ${issue.message}\n`)
  }
  process.exit(1)
} else {
  console.log('✓ UI audit passed')
  process.exit(0)
}
