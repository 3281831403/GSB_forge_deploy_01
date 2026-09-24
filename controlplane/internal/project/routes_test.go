package project_test

// HTTP-layer test: pins "every project operation is behind the cookie gate" and
// runs in CI. It uses a sentinel gate that rejects before any handler, so it
// needs no database and is not skipped under -short. Delete Middlewares from any
// route in RegisterRoutes and one operation reaches the (nil) handler instead of
// the gate, failing here.

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
)

func TestRoutesEnforceGate(t *testing.T) {
	ops := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/organizations/1/projects"},
		{http.MethodGet, "/api/v1/organizations/1/projects"},
		{http.MethodGet, "/api/v1/projects/1"},
		{http.MethodGet, "/api/v1/projects/1/health"},
		{http.MethodGet, "/api/v1/projects/1/spec"},
		{http.MethodPost, "/api/v1/projects/1/revisions"},
		{http.MethodPost, "/api/v1/projects/1/resources"},
		{http.MethodPatch, "/api/v1/projects/1/resources/res_x"},
		{http.MethodDelete, "/api/v1/projects/1/resources/res_x"},
		{http.MethodPost, "/api/v1/projects/1/resources/res_x/fields"},
		{http.MethodPatch, "/api/v1/projects/1/resources/res_x/fields/fld_y"},
		{http.MethodDelete, "/api/v1/projects/1/resources/res_x/fields/fld_y"},
		{http.MethodPost, "/api/v1/projects/1/resources/res_x/relationships"},
		{http.MethodPatch, "/api/v1/projects/1/resources/res_x/behavior"},
		{http.MethodPost, "/api/v1/projects/1/pages"},
		{http.MethodPatch, "/api/v1/projects/1/pages/pag_x/table"},
		{http.MethodPatch, "/api/v1/projects/1/pages/pag_x/form"},
		{http.MethodDelete, "/api/v1/projects/1/pages/pag_x"},
		{http.MethodPut, "/api/v1/projects/1/navigation"},
		{http.MethodPut, "/api/v1/projects/1/branding"},
	}

	var gated int
	sentinel := func(ctx huma.Context, next func(huma.Context)) {
		gated++
		ctx.SetStatus(http.StatusUnauthorized) // reject; never call next
	}
	_, api := humatest.New(t)
	// svc/authz are nil deliberately: the gate rejects before any handler runs,
	// which is exactly the invariant under test.
	project.RegisterRoutes(api, "/api/v1", huma.Middlewares{sentinel}, nil, nil)

	for _, op := range ops {
		if resp := api.Do(op.method, op.path); resp.Code != http.StatusUnauthorized {
			t.Errorf("%s %s reached the handler; gate not attached (got %d)", op.method, op.path, resp.Code)
		}
	}
	if gated != len(ops) {
		t.Errorf("gate ran %d times, want %d (some operation is ungated)", gated, len(ops))
	}
}
