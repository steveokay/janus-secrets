import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// The theme adds custom scale keys (rounded-card, shadow-elev-1/elev-2/pop) that
// stock tailwind-merge doesn't know; without this, cn() can't collapse conflicts
// in those groups and "caller's className wins" silently breaks.
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: {
      rounded: [{ rounded: ['card'] }],
      shadow: [{ shadow: ['elev-1', 'elev-2', 'pop'] }],
    },
  },
})

export const cn = (...inputs: ClassValue[]) => twMerge(clsx(inputs))
