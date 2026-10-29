// What components read their words from. Kept apart from the dictionaries
// (copy/index.ts) so the mockups, which the English examples import, don't
// import them back.
import { createContext, useContext } from 'react'

import type { Copy } from './en'

const CopyContext = createContext<Copy | null>(null)

export const CopyProvider = CopyContext.Provider

export function useCopy(): Copy {
  const copy = useContext(CopyContext)
  if (!copy) throw new Error('useCopy: no CopyProvider')
  return copy
}
