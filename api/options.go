package api

import (
	"fmt"
	"net/url"

	"go.uber.org/zap"
)

type (
	// AppOption is a function that applies an option to the application API.
	AppOption func(*applicationAPI)

	// AdminOption is a function that applies an option to the admin API.
	AdminOption func(*adminAPI)
)

// WithExplorer sets the explorer for the admin API.
func WithExplorer(e Explorer) AdminOption {
	return func(api *adminAPI) {
		api.explorer = e
	}
}

// WithAdminLogger sets the logger for admin API.
func WithAdminLogger(log *zap.Logger) AdminOption {
	return func(api *adminAPI) {
		api.log = log
	}
}

// WithDebug sets the debug mode for admin API. In debug mode, the server
// exposes additional debug endpoints that allow triggering certain actions.
func WithDebug() AdminOption {
	return func(api *adminAPI) {
		api.debug = true
	}
}

// WithAppLogger sets the logger for application API.
func WithAppLogger(log *zap.Logger) AppOption {
	return func(api *applicationAPI) {
		api.log = log
	}
}

// URLQueryParameterOption is an option to configure the query string
// parameters.
type URLQueryParameterOption func(url.Values)

// WithOffset sets the 'offset' parameter.
func WithOffset(offset int) URLQueryParameterOption {
	return func(q url.Values) {
		q.Set("offset", fmt.Sprint(offset))
	}
}

// WithLimit sets the 'limit' parameter.
func WithLimit(limit int) URLQueryParameterOption {
	return func(q url.Values) {
		q.Set("limit", fmt.Sprint(limit))
	}
}

// HostQueryParameterOption is an option to configure the query string for the
// Hosts endpoint.
type HostQueryParameterOption URLQueryParameterOption

// WithBlocked sets the 'blocked' parameter.
func WithBlocked(blocked bool) HostQueryParameterOption {
	return func(q url.Values) {
		q.Set("blocked", fmt.Sprint(blocked))
	}
}

// WithUsable sets the 'usable' parameter.
func WithUsable(usable bool) HostQueryParameterOption {
	return func(q url.Values) {
		q.Set("usable", fmt.Sprint(usable))
	}
}

// WithActiveContracts sets the 'activecontracts' parameter.
func WithActiveContracts(activeContracts bool) HostQueryParameterOption {
	return func(q url.Values) {
		q.Set("activecontracts", fmt.Sprint(activeContracts))
	}
}
