import { NavLink, Outlet } from 'react-router-dom'
import { useApiKey } from '../hooks/useApiKey'

export default function Layout() {
  const { apiKey, setApiKey } = useApiKey()

  return (
    <div className="app-shell">
      <header className="topbar">
        <h1>conTogether</h1>
        <nav>
          <NavLink to="/" end>
            Containers
          </NavLink>
          <NavLink to="/upload">Uploads</NavLink>
          <NavLink to="/app-logs">App Logs</NavLink>
        </nav>
        <input
          className="api-key-input"
          type="password"
          placeholder="X-API-Key"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          autoComplete="off"
          spellCheck={false}
        />
      </header>
      <main className="content">
        <Outlet />
      </main>
    </div>
  )
}
