package config

import (
	"time"

	"google.golang.org/genai"
)

type ModelConfig struct {
	API_KEY          string                       `yaml:"API_KEY"`
	Model            string                       `yaml:"model"`
	PrefixToCall     string                       `yaml:"prefixToCall"`
	MaxOutputToken   int32                        `yaml:"maxOutputToken"`
	AlargmTokenCount int32                        `yaml:"alargmTokenCount"`
	UseInternet      bool                         `yaml:"useInternet"`
	SecureCheck      bool                         `yaml:"secureCheck"`
	MaxMonthlySearch int                          `yaml:"maxMonthlySearch"`
	TimeOutWhen      time.Duration                `yaml:"timeOutWhen"`
	IncludeThoughts  bool                         `yaml:"includeThoughts"`
	ThinkingBudget   int32                        `yaml:"thinkingBudget"`
	ThinkingLevel    string                       `yaml:"thinkingLevel"`
	Rate             float64                      `yaml:"rate"`
	RateBurst        int                          `yaml:"rateBurst"`
	Soul             string                       `yaml:"-"`
	Config           *genai.GenerateContentConfig `yaml:"-"`
}
