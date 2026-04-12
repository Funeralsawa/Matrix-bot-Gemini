package config

import (
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type BotConfig struct {
	WorkDir string       `yaml:"-"`
	Client  ClientConfig `yaml:"CLIENT"`
	Model   ModelConfig  `yaml:"MODEL"`
}

func NewBotConfig() (cfg *BotConfig) {
	cfg = &BotConfig{}
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path. system down.")
	}
	cfg.WorkDir = filepath.Dir(exePath)

	setupEnv(cfg.WorkDir)

	configPath := filepath.Join(cfg.WorkDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Error occurred when reading bot config: %v", err)
	}
	err = yaml.Unmarshal(data, cfg)
	if err != nil {
		log.Fatalf("config.yaml can't unmarshal: %v", err)
	}
	if cfg.Client.MaxMemoryLength <= cfg.Client.WhenRetroRemainMemLen {
		log.Fatalf("config.yaml maxMemoryLength can not bigger than whenRetroRemainMemLen.")
	}
	soulPath := filepath.Join(cfg.WorkDir, "soul.md")
	soulData, err := os.ReadFile(soulPath)
	if err != nil {
		log.Fatalf("Failed to read soul.md: %v", err)
	}
	cfg.Model.Soul = string(soulData)

	return
}
