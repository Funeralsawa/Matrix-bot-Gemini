package config

import "maunium.net/go/mautrix/id"

type ClientConfig struct {
	HomeserverURL         string    `yaml:"homeserverURL"`
	UserID                id.UserID `yaml:"userID"`
	AccessToken           string    `yaml:"accessToken"`
	DeviceID              id.UserID `yaml:"deviceID"`
	LogRoom               []string  `yaml:"logRoom"`
	MaxMemoryLength       int       `yaml:"maxMemoryLength"`
	WhenRetroRemainMemLen int       `yaml:"whenRetroRemainMemLen"`
	AvatarURL             string    `yaml:"avatarURL"`
	DisplayName           string    `yaml:"displayName"`
	DatabasePassword      string    `yaml:"databasePassword"`
}
