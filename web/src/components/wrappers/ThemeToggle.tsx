import { useState } from 'react'
import { Moon, Sun } from 'lucide-react'
import { IconButton } from '@/components/wrappers/IconButton'

/**
 * Flips the `dark` class on the root and remembers the choice. index.html sets
 * the class before first paint, so the initial state is read straight from it.
 * Light and dark are one theme, not a second design (UI.md §5).
 */
export function ThemeToggle() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))

  const toggle = () => {
    const next = !dark
    setDark(next)
    document.documentElement.classList.toggle('dark', next)
    try {
      localStorage.setItem('theme', next ? 'dark' : 'light')
    } catch {
      /* private mode */
    }
  }

  return (
    <IconButton label={dark ? 'Use the light theme' : 'Use the dark theme'} onClick={toggle}>
      {dark ? <Moon /> : <Sun />}
    </IconButton>
  )
}
