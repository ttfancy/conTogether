import { useContext } from 'react'
import { ApiKeyContext, type ApiKeyContextValue } from '../context/apiKeyContextInternal'

export function useApiKey(): ApiKeyContextValue {
  const ctx = useContext(ApiKeyContext)
  if (!ctx) throw new Error('useApiKey must be used within ApiKeyProvider')
  return ctx
}
