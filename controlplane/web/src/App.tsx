import { Navigate, Route, Routes } from "react-router-dom";
import { useSession } from "./auth/session";
import { LoginView } from "./auth/LoginView";
import { AppShell } from "./shell/AppShell";
import { AREAS, AreaPlaceholder, DEFAULT_AREA } from "./shell/areas";

// App is the top-level gate: while the session probe is in flight it shows a
// neutral loading state; an anonymous session gets the sign-in view; an
// authenticated one gets the editor shell with a route per area, defaulting to
// the Data area.
export function App() {
  const { status } = useSession();

  if (status === "loading") {
    return (
      <main className="centered" aria-busy="true">
        <p>Loading…</p>
      </main>
    );
  }

  if (status === "anonymous") {
    return <LoginView />;
  }

  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<Navigate to={`/${DEFAULT_AREA.path}`} replace />} />
        {AREAS.map((area) => (
          <Route key={area.path} path={area.path} element={area.element ?? <AreaPlaceholder area={area} />} />
        ))}
        <Route path="*" element={<Navigate to={`/${DEFAULT_AREA.path}`} replace />} />
      </Route>
    </Routes>
  );
}
