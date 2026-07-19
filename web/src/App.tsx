import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { ApiKeyProvider } from './context/ApiKeyContext'
import { ToastProvider } from './context/ToastContext'
import Layout from './components/Layout'
import ContainersPage from './pages/ContainersPage'
import ContainerLogsPage from './pages/ContainerLogsPage'
import ContainerExecPage from './pages/ContainerExecPage'
import UploadsPage from './pages/UploadsPage'
import AppLogsPage from './pages/AppLogsPage'

export default function App() {
  return (
    <ToastProvider>
      <ApiKeyProvider>
        <BrowserRouter>
          <Routes>
            <Route element={<Layout />}>
              <Route index element={<ContainersPage />} />
              <Route path="containers/:id/logs" element={<ContainerLogsPage />} />
              <Route path="containers/:id/exec" element={<ContainerExecPage />} />
              {/* /upload and /app-logs, not /uploads or /logs: those are
                  exact-match backend API paths (POST /uploads, GET/DELETE
                  /logs). container-api's router does exact-path matching,
                  so a same-named frontend route would never reach the
                  SPA-fallback NoRoute handler in production — it'd always
                  hit the real API handler instead. */}
              <Route path="upload" element={<UploadsPage />} />
              <Route path="app-logs" element={<AppLogsPage />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </ApiKeyProvider>
    </ToastProvider>
  )
}
