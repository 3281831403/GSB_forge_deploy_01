import { NavLink, Outlet } from "react-router-dom";
import { useSession } from "../auth/session";
import { AREAS } from "./areas";

// The authenticated app shell: a header naming the product and the current user,
// primary navigation across the four editor areas, and an outlet the active
// area renders into.
export function AppShell() {
  const { user, logout } = useSession();

  return (
    <div className="shell">
      <header className="shell-header">
        <span className="brand">Forge</span>
        <nav className="areas" aria-label="Editor areas">
          {AREAS.map((area) => (
            <NavLink
              key={area.path}
              to={`/${area.path}`}
              className={({ isActive }) => (isActive ? "area-link active" : "area-link")}
            >
              {area.label}
            </NavLink>
          ))}
        </nav>
        <div className="session">
          {user && <span className="user-email">{user.email}</span>}
          <button type="button" onClick={() => void logout()}>
            Sign out
          </button>
        </div>
      </header>
      <main className="shell-main">
        <Outlet />
      </main>
    </div>
  );
}
