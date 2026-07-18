import { createContext } from 'react'

export type ToastKind = 'error' | 'success' | 'info'

export interface ToastContextValue {
  // Fire-and-forget: callers don't need the toast's id or lifecycle,
  // just "show the user this, non-blockingly." Compare to
  // window.alert(), which halts JS execution and looks out of place —
  // this is the polished equivalent every action-triggered error/success
  // in the app should use instead of a raw inline banner (banners stay
  // reserved for "this whole page failed to load" states, which need to
  // persist rather than fade after a few seconds).
  showToast: (message: string, kind?: ToastKind) => void
}

export const ToastContext = createContext<ToastContextValue | null>(null)
