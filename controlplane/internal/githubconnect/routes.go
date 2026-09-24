package githubconnect

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gombit-dev/gombit/auth"
	"github.com/gombit-dev/gombit/contract"
	"github.com/gombit-dev/gombit/framework"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubexport"
)

// cookieSecurityName is the OpenAPI security scheme the admin plane also uses;
// naming it here documents that these routes are cookie-session protected.
const cookieSecurityName = "cookieAuth"

// Register mounts the GitHub OAuth connect flow on the app behind the cookie
// gate, using cfg for the OAuth client and redirecting to successRedirect after
// a completed connect. It is called from main only when GitHub OAuth is
// configured — with no credentials the control plane runs without the feature.
func Register(app *framework.App, cfg githubexport.Config, successRedirect string) error {
	authSvc, err := auth.NewService(app.DB(), app.Config())
	if err != nil {
		return err
	}
	svc := NewService(app.DB(), NewExchanger(cfg, nil))
	RegisterRoutes(app.API(), app.Config().API.Prefix,
		huma.Middlewares{authSvc.RequireCookieSession()}, svc, successRedirect)
	return nil
}

// RegisterRoutes wires the two connect operations onto api behind gate, serving
// svc. It is separated from Register (which supplies api/gate/svc from a
// framework.App) so a test can mount the real routes and cookie gate on a
// humatest API without standing up a full framework.App.
//
// Both operations are GET — the OAuth redirect and its callback — so they are
// safe methods that carry no CSRF token, and the browser presents the session
// cookie on the callback, which is how the completed connection is bound to the
// caller rather than to whoever forged a state.
func RegisterRoutes(api huma.API, prefix string, gate huma.Middlewares, svc *Service, successRedirect string) {
	h := &handler{svc: svc, successRedirect: successRedirect}
	security := []map[string][]string{{cookieSecurityName: {}}}
	tags := []string{"GitHub"}

	huma.Register(api, huma.Operation{
		OperationID:   "github-connect-start",
		Method:        http.MethodGet,
		Path:          prefix + "/integrations/github/connect",
		Summary:       "Start the GitHub OAuth connect flow",
		Description:   "Redirects the authenticated user to GitHub to authorize repository access.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusFound,
	}, h.start)

	huma.Register(api, huma.Operation{
		OperationID:   "github-connect-callback",
		Method:        http.MethodGet,
		Path:          prefix + "/integrations/github/callback",
		Summary:       "Complete the GitHub OAuth connect flow",
		Description:   "GitHub redirects here with a code and state; the code is exchanged for a token and the connection stored.",
		Tags:          tags,
		Security:      security,
		Middlewares:   gate,
		DefaultStatus: http.StatusFound,
	}, h.callback)
}

type handler struct {
	svc *Service
	// successRedirect is where the callback sends the browser after a completed
	// connect (e.g. the editor's integrations page).
	successRedirect string
}

// redirectOutput is a bodyless 3xx: Huma sets the status from the operation's
// DefaultStatus and emits the Location header from this field.
type redirectOutput struct {
	Location string `header:"Location"`
}

type startInput struct{}

func (h *handler) start(ctx context.Context, _ *startInput) (*redirectOutput, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, contract.WithContext(ctx, contract.Authentication("authentication required"))
	}
	url, err := h.svc.StartConnect(ctx, user.ID)
	if err != nil {
		return nil, contract.WithContext(ctx, contract.Internal("could not start the github connect flow"))
	}
	return &redirectOutput{Location: url}, nil
}

type callbackInput struct {
	Code  string `query:"code" doc:"OAuth authorization code from GitHub"`
	State string `query:"state" doc:"Anti-CSRF state minted by the connect start"`
}

func (h *handler) callback(ctx context.Context, in *callbackInput) (*redirectOutput, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, contract.WithContext(ctx, contract.Authentication("authentication required"))
	}
	if in.Code == "" || in.State == "" {
		return nil, contract.WithContext(ctx, contract.Validation("the callback requires code and state", map[string][]string{
			"query": {"code and state are required"},
		}))
	}
	if err := h.svc.CompleteConnect(ctx, user.ID, in.State, in.Code); err != nil {
		// A bad, expired, replayed, or cross-user state is a client-visible
		// validation failure, not a server fault; anything else is internal.
		if errors.Is(err, ErrInvalidState) {
			return nil, contract.WithContext(ctx, contract.Validation("invalid or expired oauth state", map[string][]string{
				"state": {"the oauth state is missing, expired, or already used"},
			}))
		}
		return nil, contract.WithContext(ctx, contract.Internal("could not complete the github connect flow"))
	}
	return &redirectOutput{Location: h.successRedirect}, nil
}
