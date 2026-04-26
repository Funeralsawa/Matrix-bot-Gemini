package config

import "maunium.net/go/mautrix/id"

type AuthConfig struct {
	AdminID []id.UserID `yaml:"adminID"`
}
