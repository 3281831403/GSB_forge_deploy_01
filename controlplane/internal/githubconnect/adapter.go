package githubconnect

import (
	"context"
	"net/http"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubexport"
)

// exchanger is the production Exchanger: it adapts internal/githubexport's OAuth
// calls (AuthorizeURL + ExchangeCode) to the surface the connect service needs,
// so the service stays testable with a fake and never imports the HTTP client
// directly. It mirrors ghexport's Publisher adapter — both wrap the same thin
// githubexport client over one shared Config.
type exchanger struct {
	cfg    githubexport.Config
	client *http.Client
}

// NewExchanger builds the production Exchanger over the GitHub OAuth config. A
// nil client uses http.DefaultClient.
func NewExchanger(cfg githubexport.Config, client *http.Client) Exchanger {
	if client == nil {
		client = http.DefaultClient
	}
	return exchanger{cfg: cfg, client: client}
}

func (e exchanger) AuthorizeURL(state string) string {
	return githubexport.AuthorizeURL(e.cfg, state)
}

func (e exchanger) ExchangeCode(ctx context.Context, code string) (string, error) {
	return githubexport.ExchangeCode(ctx, e.client, e.cfg, code)
}
