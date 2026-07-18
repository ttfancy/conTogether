import { useMemo, useState, type ReactNode } from 'react'
import { getApiKey, setStoredApiKey } from '../api/client'
import { ApiKeyContext, type ApiKeyContextValue } from './apiKeyContextInternal'

export function ApiKeyProvider({ children }: { children: ReactNode }) {
  const [apiKey, setApiKeyState] = useState(getApiKey)

  const value = useMemo<ApiKeyContextValue>(
    () => ({
      apiKey,
      setApiKey: (key: string) => {
        setStoredApiKey(key)
        setApiKeyState(key)
      },
    }),
    [apiKey],
  )

  return <ApiKeyContext.Provider value={value}>{children}</ApiKeyContext.Provider>
}
