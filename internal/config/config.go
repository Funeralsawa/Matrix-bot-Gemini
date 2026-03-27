package config

import (
	"time"

	"google.golang.org/genai"
	"maunium.net/go/mautrix/id"
)

type BotConfig struct {
	Client ClientConfig `yaml:"CLIENT"`
	Model  ModelConfig  `yaml:"MODEL"`
}

type ClientConfig struct {
	HomeserverURL   string    `yaml:"homeserverURL"`
	UserID          id.UserID `yaml:"userID"`
	AccessToken     string    `yaml:"accessToken"`
	DeviceID        id.UserID `yaml:"deviceID"`
	LogRoom         []string  `yaml:"logRoom"`
	MaxMemoryLength int       `yaml:"maxMemoryLength"`
}

type ModelConfig struct {
	API_KEY          string                       `yaml:"API_KEY"`
	Model            string                       `yaml:"model"`
	MaxOutputToken   int32                        `yaml:"maxOutputToken"`
	AlargmTokenCount int32                        `yaml:"alargmTokenCount"`
	UseInternet      bool                         `yaml:"useInternet"`
	SecureCheck      bool                         `yaml:"secureCheck"`
	MaxMonthlySearch int                          `yaml:"maxMonthlySearch"`
	TimeOutWhen      time.Duration                `yaml:"timeOutWhen"`
	DatabasePassword string                       `yaml:"databasePassword"`
	Soul             string                       `yaml:"-"`
	Config           *genai.GenerateContentConfig `yaml:"-"`
}
