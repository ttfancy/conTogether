import { createContext } from 'react'

export interface ApiKeyContextValue {
  apiKey: string
  setApiKey: (key: string) => void
}

export const ApiKeyContext = createContext<ApiKeyContextValue | null>(null)
