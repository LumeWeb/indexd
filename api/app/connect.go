package app

import "time"

type (
	// A ConnectKey represents a key used to authenticate
	// when connecting a new application.
	ConnectKey struct {
		Key           string
		Description   string
		RemainingUses int
		DateCreated   time.Time
	}

	// AddConnectKey represents a request to add or update
	// an app connect key.
	AddConnectKey struct {
		Key           string
		Description   string
		RemainingUses int
	}
)
