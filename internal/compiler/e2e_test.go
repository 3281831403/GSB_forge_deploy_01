package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gombit-dev/gombit-forge/internal/gombit"
)

// TestM0EndToEnd is the M0 go/no-go gate (DESIGN.md §31–32): from a fixed
// two-resource ProjectSpec, generate a Gombit application, apply its migration
// on Postgres, build and boot it, then exercise the real contract over HTTP —
// create a customer and read it back, create an invoice with the belongs_to
// foreign key and a decimal that round-trips, and confirm the admin catalog
// lists the admin-visible resource and not the hidden one (so RegisterAll
// actually ran RegisterAdmin) — all with no Forge runtime dependency.
//
// It orchestrates the real toolchain and a throwaway Postgres, so it needs
// gombit, atlas, go and docker, and skips in -short or when any is absent. The
// determinism, structure and per-stage contracts are covered by the unit tests;
// this proves they compose into a working application.
func TestM0EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end harness in -short")
	}
	for _, bin := range []string{gombit.DefaultBinary, "atlas", "go", "docker"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
	cli := &gombit.CLI{}
	version, err := cli.Version(context.Background())
	if err != nil {
		t.Fatalf("gombit version: %v", err)
	}
	if err := gombit.CheckSupported(version); err != nil {
		t.Skipf("installed toolchain unsupported: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const module = "example.com/app"
	dir := filepath.Join(t.TempDir(), "app")

	// --- scaffold + generate ------------------------------------------------
	if err := cli.Scaffold(ctx, gombit.ScaffoldRequest{
		Dir: dir, Name: "app", Module: module,
		Database: gombit.DatabasePostgres, Auth: gombit.AuthCookie, UI: gombit.UIMinimal,
		Tidy: true,
	}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	s := sampleSpec(t)
	files, err := Compile(s, module)
	if err != nil {
		t.Fatalf("compile app: %v", err)
	}
	for _, f := range files {
		writeAppFile(t, dir, f.Path, f.Content)
	}
	// Forge owns the composition root: main wires every resource through
	// RegisterAll and applies migrations out of band, never AutoMigrate (§14).
	// The server no longer registers the scaffold's demo resource; its package
	// stays in the tree (the management CLI still imports it) but is not mounted.
	writeAppFile(t, dir, CompositionRootPath, mustCompositionRoot(t, module))

	runCmd(t, ctx, dir, nil, "go", "mod", "tidy")

	// --- no Forge runtime dependency (D2/P5) --------------------------------
	assertNoForgeDependency(t, dir)

	// --- migration ----------------------------------------------------------
	models, err := MigrationModelsForSpec(s, module)
	if err != nil {
		t.Fatalf("migration models: %v", err)
	}
	if err := cli.MakeMigrations(ctx, gombit.MakeMigrationsRequest{
		Dir: dir, Name: "initial", Driver: gombit.DatabasePostgres, Models: models,
	}); err != nil {
		t.Fatalf("makemigrations: %v", err)
	}

	// --- Postgres + apply ---------------------------------------------------
	pg := startPostgres(t, ctx)
	defer pg.stop()

	env := []string{
		"GOMBIT_DATABASE_DRIVER=postgres",
		"GOMBIT_DATABASE_DSN=" + pg.dsn,
		// Cookie-mode auth for the admin plane and CSRF; the secret also signs
		// sessions. Cookies are insecure because the test speaks plain HTTP.
		"GOMBIT_JWT_SECRET=forge-e2e-secret-please-change-0001",
		"GOMBIT_AUTH_MODE=cookie",
		"GOMBIT_COOKIE_SECURE=false",
		"GOMBIT_COOKIE_SAMESITE=Lax",
	}
	runCmd(t, ctx, dir, env, gombit.DefaultBinary, "db", "migrate")

	// Migration applied: the tables now exist.
	assertTableExists(t, ctx, pg, "customers")
	assertTableExists(t, ctx, pg, "invoices")

	// --- build + boot -------------------------------------------------------
	bin := filepath.Join(dir, "server")
	runCmd(t, ctx, dir, nil, "go", "build", "-o", bin, "./cmd/server")

	port := freePort(t)
	appEnv := append(env, "GOMBIT_HTTP_ADDR=127.0.0.1:"+port)
	server := startServer(t, ctx, dir, bin, appEnv)
	defer server.stop()

	base := "http://127.0.0.1:" + port
	waitForHTTP(t, base+"/livez")

	// --- exercise the real contract ----------------------------------------
	// An authenticated session, since cookie mode gates writes with CSRF and
	// the admin plane with a superuser.
	runCmd(t, ctx, dir, appEnv, gombit.DefaultBinary, "createsuperuser",
		"--no-input", "--email", superuserEmail, "--password", superuserPassword)

	session := login(t, base)

	// React CRUD works: create a customer, read it back. (sampleSpec's customer
	// is email + active.)
	customerID := session.create(t, base+"/api/v1/customers", map[string]any{
		"email":  "e2e@example.test",
		"active": true,
	})
	got := session.get(t, base+"/api/v1/customers/"+customerID)
	if got["email"] != "e2e@example.test" {
		t.Errorf("read-back customer email = %v, want e2e@example.test", got["email"])
	}

	// belongs_to and decimal round-trip: an invoice referencing the customer.
	fkNum, err := strconvAtoi(customerID)
	if err != nil {
		t.Fatalf("customer id %q is not numeric: %v", customerID, err)
	}
	invoiceID := session.create(t, base+"/api/v1/invoices", map[string]any{
		"customer_id": fkNum,
		"total":       "19.95",
	})
	invoice := session.get(t, base+"/api/v1/invoices/"+invoiceID)
	if fmt.Sprint(invoice["customer_id"]) != customerID {
		t.Errorf("invoice customer_id = %v, want %s", invoice["customer_id"], customerID)
	}
	if invoice["total"] != "19.95" {
		t.Errorf("invoice total = %v, want 19.95 (decimal round-trip)", invoice["total"])
	}

	// Server-side numeric aggregates (#182, gombit #273): Invoice.Total is
	// declared aggregatable, so the generated list handler honors
	// ?aggregate=<op>:<field> and returns meta.aggregates keyed "<op>:<field>".
	// With one invoice of 19.95, SUM and MAX both equal 19.95 — proving the
	// generated ListMeta/ParseAggregates/Aggregate wiring runs against real Gombit.
	meta := session.listMeta(t, base+"/api/v1/invoices?aggregate=sum:total,max:total")
	aggs, ok := meta["aggregates"].(map[string]any)
	if !ok {
		t.Fatalf("list meta missing aggregates object; got meta=%v", meta)
	}
	if fmt.Sprint(aggs["sum:total"]) != "19.95" {
		t.Errorf("aggregate sum:total = %v, want 19.95", aggs["sum:total"])
	}
	if fmt.Sprint(aggs["max:total"]) != "19.95" {
		t.Errorf("aggregate max:total = %v, want 19.95", aggs["max:total"])
	}

	// Admin works, and RegisterAll actually ran RegisterAdmin: the admin
	// catalog lists the admin-visible customer but not the hidden invoice.
	slugs := session.adminSlugs(t, base+"/api/v1/admin/meta")
	if !slugs["customers"] {
		t.Errorf("admin meta must list customers (RegisterAdmin did not run); got %v", slugs)
	}
	if slugs["invoices"] {
		t.Errorf("admin meta must not list invoices (not admin-visible); got %v", slugs)
	}

	t.Logf("M0 gate cleared: created customer %s + invoice %s, admin catalog=%v", customerID, invoiceID, keysOf(slugs))
}

// mustCompositionRoot generates the production composition root for the module,
// so the end-to-end tests build and boot exactly what Forge ships rather than a
// hand-maintained copy.
func mustCompositionRoot(t *testing.T, module string) []byte {
	t.Helper()
	src, err := CompositionRoot(module)
	if err != nil {
		t.Fatalf("composition root: %v", err)
	}
	return src
}

func writeAppFile(t *testing.T, dir, rel string, content []byte) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func runCmd(t *testing.T, ctx context.Context, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s:\n%s", name, strings.Join(args, " "), out)
	}
}

// assertNoForgeDependency confirms the generated application has no compile-time
// dependency on Forge (D2/P5): it must keep working if Forge disappears.
func assertNoForgeDependency(t *testing.T, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") && filepath.Base(path) != "go.mod" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("gombit-forge")) {
			rel, _ := filepath.Rel(dir, path)
			t.Errorf("%s references gombit-forge; the app must not depend on Forge", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

const (
	superuserEmail    = "admin@e2e.test"
	superuserPassword = "Password123!"
)

// session is an authenticated cookie-mode client: a cookie jar plus the CSRF
// token cookie mode requires on writes.
type session struct {
	client *http.Client
	csrf   string
}

func login(t *testing.T, base string) *session {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	c := &http.Client{Jar: jar, Timeout: 15 * time.Second}

	csrf := fetchCSRF(t, c, base)
	// Log in; the response sets the HttpOnly session cookie in the jar.
	post(t, c, base+"/api/v1/auth/login", csrf, map[string]any{
		"email": superuserEmail, "password": superuserPassword,
	})
	// Re-fetch the CSRF token now that the session cookie is set.
	return &session{client: c, csrf: fetchCSRF(t, c, base)}
}

func fetchCSRF(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	resp, err := c.Get(base + "/api/v1/auth/csrf")
	if err != nil {
		t.Fatalf("GET csrf: %v", err)
	}
	data := decodeData(t, readBody(t, resp))
	token, _ := data["csrf_token"].(string)
	if token == "" {
		t.Fatalf("no csrf_token in response: %v", data)
	}
	return token
}

// create POSTs a resource and returns its id as a string.
func (s *session) create(t *testing.T, url string, body map[string]any) string {
	t.Helper()
	data := decodeData(t, post(t, s.client, url, s.csrf, body))
	if data["id"] == nil {
		t.Fatalf("create %s: no id in response %v", url, data)
	}
	return fmt.Sprint(numericID(t, data["id"]))
}

func (s *session) get(t *testing.T, url string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d\n%s", url, resp.StatusCode, readBody(t, resp))
	}
	return decodeData(t, mustRead(t, resp))
}

// listMeta GETs a collection and returns its response meta object — pagination
// info plus any server-side aggregates (meta.aggregates).
func (s *session) listMeta(t *testing.T, url string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d\n%s", url, resp.StatusCode, readBody(t, resp))
	}
	var env map[string]any
	if err := json.Unmarshal(mustRead(t, resp), &env); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	meta, _ := env["meta"].(map[string]any)
	return meta
}

// adminSlugs returns the set of resource slugs the admin catalog lists.
func (s *session) adminSlugs(t *testing.T, url string) map[string]bool {
	t.Helper()
	data := s.get(t, url)
	slugs := map[string]bool{}
	models, _ := data["models"].([]any)
	for _, m := range models {
		if entry, ok := m.(map[string]any); ok {
			if slug, ok := entry["slug"].(string); ok {
				slugs[slug] = true
			}
		}
	}
	return slugs
}

// post sends a JSON body with the CSRF header and returns the response bytes,
// failing on a non-2xx status.
func post(t *testing.T, c *http.Client, url, csrf string, body map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	data := mustRead(t, resp)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("POST %s: status %d\n%s", url, resp.StatusCode, data)
	}
	return data
}

// decodeData unwraps the D10 {data: ...} envelope, tolerating a bare object.
func decodeData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode json: %v\n%s", err, body)
	}
	if d, ok := env["data"].(map[string]any); ok {
		return d
	}
	return env
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}

func mustRead(t *testing.T, resp *http.Response) []byte { return readBody(t, resp) }

// numericID normalizes a JSON id (number or string) to an integer.
func numericID(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, err := strconvAtoi(n)
		if err != nil {
			t.Fatalf("id %q not numeric: %v", n, err)
		}
		return i
	default:
		t.Fatalf("unexpected id type %T", v)
		return 0
	}
}

func strconvAtoi(s string) (int, error) { return strconv.Atoi(s) }

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

// serverProc is a running application process.
type serverProc struct {
	cmd *exec.Cmd
	buf *bytes.Buffer
	t   *testing.T
}

func startServer(t *testing.T, ctx context.Context, dir, bin string, env []string) *serverProc {
	t.Helper()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	buf := &bytes.Buffer{}
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

// pgContainer is a throwaway Postgres run under docker.
type pgContainer struct {
	id  string
	dsn string
	t   *testing.T
}

func startPostgres(t *testing.T, ctx context.Context) *pgContainer {
	t.Helper()
	port := freePort(t)
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--rm",
		"-e", "POSTGRES_PASSWORD=forge",
		"-e", "POSTGRES_DB=app",
		"-p", "127.0.0.1:"+port+":5432",
		"postgres:15",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run postgres:\n%s", out)
	}
	id := strings.TrimSpace(string(out))
	pg := &pgContainer{
		id:  id,
		dsn: fmt.Sprintf("postgres://postgres:forge@127.0.0.1:%s/app?sslmode=disable", port),
		t:   t,
	}

	// Wait for readiness.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if err := exec.CommandContext(ctx, "docker", "exec", id, "pg_isready", "-U", "postgres").Run(); err == nil {
			// pg_isready can pass a moment before connections are accepted.
			time.Sleep(time.Second)
			return pg
		}
		time.Sleep(500 * time.Millisecond)
	}
	pg.stop()
	t.Fatal("postgres did not become ready")
	return nil
}

func (p *pgContainer) stop() {
	if p.id != "" {
		_ = exec.Command("docker", "stop", p.id).Run()
	}
}

func assertTableExists(t *testing.T, ctx context.Context, pg *pgContainer, table string) {
	t.Helper()
	query := fmt.Sprintf("SELECT to_regclass('public.%s');", table)
	out, err := exec.CommandContext(ctx, "docker", "exec", pg.id,
		"psql", "-U", "postgres", "-d", "app", "-tAc", query).CombinedOutput()
	if err != nil {
		t.Fatalf("psql %s: %v\n%s", table, err, out)
	}
	if strings.TrimSpace(string(out)) != table {
		t.Errorf("table %s was not created by the migration (got %q)", table, strings.TrimSpace(string(out)))
	}
}
