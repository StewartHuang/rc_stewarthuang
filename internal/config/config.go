package config

import (
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Worker    WorkerConfig    `yaml:"worker"`
	Suppliers []SupplierEntry `yaml:"suppliers"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type WorkerConfig struct {
	PollInterval   string `yaml:"poll_interval"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	HTTPTimeout    string `yaml:"http_timeout"`
}

type SupplierEntry struct {
	Name             string            `yaml:"name"`
	URL              string            `yaml:"url"`
	Method           string            `yaml:"method"`
	Headers          map[string]string `yaml:"headers"`
	Retry            RetryConfig       `yaml:"retry"`
	AcceptedStatuses []int             `yaml:"accepted_statuses"`
}

type RetryConfig struct {
	MaxAttempts int    `yaml:"max_attempts"`
	BaseDelay   string `yaml:"base_delay"`
	MaxDelay    string `yaml:"max_delay"`
}

var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = resolveEnvVars(data)
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	setDefaults(&cfg)
	return &cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "./delivery.db"
	}
	if cfg.Worker.PollInterval == "" {
		cfg.Worker.PollInterval = "500ms"
	}
	if cfg.Worker.MaxConcurrency == 0 {
		cfg.Worker.MaxConcurrency = 10
	}
	if cfg.Worker.HTTPTimeout == "" {
		cfg.Worker.HTTPTimeout = "30s"
	}
}

func resolveEnvVars(data []byte) []byte {
	return envPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		envVar := string(match[2 : len(match)-1])
		val := os.Getenv(envVar)
		if val == "" {
			return match
		}
		return []byte(val)
	})
}
