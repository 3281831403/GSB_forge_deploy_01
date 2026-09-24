package controlplane_test

// Boot test for the control-plane bootstrap (issue #35): build the server,
// point it at a throwaway Postgres, boot it in cookie-auth mode, and prove over
// HTTP that (1) it is alive, (2) the framework-owned admin SPA is served at
// /admin/ (the #35 acceptance path), and (3) the admin data plane at
// /api/v1/admin/meta is mounted and gated by the session cookie. This is the
// dogfooding claim of DESIGN.md §6/D7 reduced to something a CI machine can
// check: Forge's control plane is an ordinary Gombit application, standing up
// with the framework's own cookie auth and admin.
//
// Like the M0 end-to-end harness it drives real infrastructure, so it skips in
// -short or when go/docker are absent. It deliberately does not exercise login:
// that needs a User row, which the tenancy tests cover; here a 401 (not a 404)
// from the admin API is the honest bootstrap signal — the route exists and the
// cookie gate is active — without any authenticated session.

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/dbtest"
)

func TestControlPlaneBoots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping control-plane boot harness in -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build the server binary from this module.
	moduleDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "server")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/server")
	build.Dir = moduleDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/server:\n%s", out)
	}

	pg := dbtest.StartPostgres(t) // skips when docker is absent

	port := dbtest.FreePort(t)
	env := append(os.Environ(),
		"GOMBIT_DATABASE_DRIVER=postgres",
		"GOMBIT_DATABASE_DSN="+pg.DSN,
		// Cookie mode: the secret signs sessions and the CSRF token. Cookies are
		// insecure because the test speaks plain HTTP.
		"GOMBIT_JWT_SECRET=control-plane-boot-secret-please-change-0001",
		"GOMBIT_AUTH_MODE=cookie",
		"GOMBIT_COOKIE_SECURE=false",
		"GOMBIT_COOKIE_SAMESITE=lax",
		"GOMBIT_HTTP_ADDR=127.0.0.1:"+port,
	)

	srv := startServer(t, ctx, bin, env)
	defer srv.stop()

	base := "http://127.0.0.1:" + port
	waitForHTTP(t, base+"/livez")

	// The app is alive.
	if code := statusOf(t, base+"/livez"); code != http.StatusOK {
		t.Errorf("GET /livez = %d, want 200", code)
	}

	// #35's acceptance path: /admin/. In cookie mode framework.New mounts the
	// framework-owned admin SPA (gombit's internal/adminui) at /admin/,
	// independently of the app's own frontend — so the control plane serves it
	// now, with no embed of ours. This asserts the criterion directly, not a
	// proxy for it.
	adminCode, adminType := getWithContentType(t, base+"/admin/")
	if adminCode != http.StatusOK {
		t.Errorf("GET /admin/ = %d, want 200 (framework admin SPA)", adminCode)
	}
	if !strings.Contains(adminType, "text/html") {
		t.Errorf("GET /admin/ Content-Type = %q, want text/html", adminType)
	}

	// The admin data plane is mounted and gated by the session cookie:
	// unauthenticated it must be rejected, not missing. A 404 would mean the
	// plane never mounted; a 200 would mean the cookie gate is off.
	code := statusOf(t, base+"/api/v1/admin/meta")
	if code == http.StatusNotFound {
		t.Errorf("GET /api/v1/admin/meta = 404: admin data plane not mounted")
	}
	if code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/admin/meta = %d, want 401 (cookie auth gate active)", code)
	}
}

func statusOf(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// getWithContentType GETs url and returns its status code and Content-Type.
func getWithContentType(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Content-Type")
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("app did not become healthy at %s", url)
}

// serverProc is a running control-plane process.
type serverProc struct {
	cmd *exec.Cmd
	buf *strings.Builder
	t   *testing.T
}

func startServer(t *testing.T, ctx context.Context, bin string, env []string) *serverProc {
	t.Helper()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = env
	buf := &strings.Builder{}
	cmd.Stdout, cmd.Stderr = buf, buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	return &serverProc{cmd: cmd, buf: buf, t: t}
}

func (s *serverProc) stop() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	if s.t.Failed() {
		s.t.Logf("server output:\n%s", s.buf.String())
	}
}
