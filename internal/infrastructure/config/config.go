package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration.
type Config struct {
	Server   Server   `yaml:"server"`
	Temporal Temporal `yaml:"temporal"`
	Auth     Auth     `yaml:"auth"`
	ArgoCD   ArgoCD   `yaml:"argocd"`
	OCI      OCI      `yaml:"oci"`
	GitHub   GitHub   `yaml:"github"`
	DocDB    DocDB    `yaml:"documentdb"`
	OTel     OTel     `yaml:"otel"`
	Policies Policies `yaml:"policies"`
	Deploy   Deploy   `yaml:"deploymentDefaults"`
}

type Server struct {
	Addr         string        `yaml:"addr"`
	ReadTimeout  time.Duration `yaml:"readTimeout"`
	WriteTimeout time.Duration `yaml:"writeTimeout"`
}

type Temporal struct {
	HostPort  string `yaml:"hostPort"`
	Namespace string `yaml:"namespace"`
	TaskQueue string `yaml:"taskQueue"`
}

type Auth struct {
	OIDC                OIDC     `yaml:"oidc"`
	AllowedRepositories []string `yaml:"allowedRepositories"`
}

type OIDC struct {
	Issuer       string        `yaml:"issuer"`
	Audience     string        `yaml:"audience"`
	JWKSCacheTTL time.Duration `yaml:"jwksCacheTTL"`
}

type ArgoCD struct {
	ServerURL string `yaml:"serverURL"`
	Token     string `yaml:"-"`
}

type OCI struct {
	Registry         string `yaml:"registry"`
	RepositoryPrefix string `yaml:"repositoryPrefix"`
}

type GitHub struct {
	Token string `yaml:"-"`
}

type DocDB struct {
	ConnectionString      string `yaml:"-"`
	Database              string `yaml:"database"`
	DeploymentsCollection string `yaml:"deploymentsCollection"`
}

type OTel struct {
	ServiceName      string  `yaml:"serviceName"`
	OTLPEndpoint     string  `yaml:"otlpEndpoint"`
	TracesSampleRate float64 `yaml:"tracesSampleRate"`
	Insecure         bool    `yaml:"insecure"`
}

type Policies struct {
	ConfigPath string `yaml:"configPath"`
}

type Deploy struct {
	SyncTimeoutSeconds              int `yaml:"syncTimeoutSeconds"`
	HealthConvergenceTimeoutSeconds int `yaml:"healthConvergenceTimeoutSeconds"`
	WorkflowTimeoutMinutes          int `yaml:"workflowTimeoutMinutes"`
	PollIntervalSeconds             int `yaml:"pollIntervalSeconds"`
}

// Load reads and parses a config file, expanding environment variables.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func applyDefaults(c *Config) {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 30 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 60 * time.Second
	}
	if c.Temporal.Namespace == "" {
		c.Temporal.Namespace = "default"
	}
	if c.Temporal.TaskQueue == "" {
		c.Temporal.TaskQueue = "deployment-workers"
	}
	if c.Auth.OIDC.Issuer == "" {
		c.Auth.OIDC.Issuer = "https://token.actions.githubusercontent.com"
	}
	if c.Auth.OIDC.JWKSCacheTTL == 0 {
		c.Auth.OIDC.JWKSCacheTTL = 24 * time.Hour
	}
	if c.OTel.TracesSampleRate == 0 {
		c.OTel.TracesSampleRate = 1.0
	}
	if c.Deploy.SyncTimeoutSeconds == 0 {
		c.Deploy.SyncTimeoutSeconds = 600
	}
	if c.Deploy.HealthConvergenceTimeoutSeconds == 0 {
		c.Deploy.HealthConvergenceTimeoutSeconds = 300
	}
	if c.Deploy.WorkflowTimeoutMinutes == 0 {
		c.Deploy.WorkflowTimeoutMinutes = 20
	}
	if c.Deploy.PollIntervalSeconds == 0 {
		c.Deploy.PollIntervalSeconds = 10
	}
	if c.DocDB.DeploymentsCollection == "" {
		c.DocDB.DeploymentsCollection = "deployments"
	}
}

func validate(c *Config) error {
	if c.Temporal.HostPort == "" {
		return fmt.Errorf("temporal.hostPort is required")
	}
	if c.Auth.OIDC.Audience == "" {
		return fmt.Errorf("auth.oidc.audience is required")
	}
	if c.ArgoCD.ServerURL == "" {
		return fmt.Errorf("argocd.serverURL is required")
	}
	if c.OCI.Registry == "" {
		return fmt.Errorf("oci.registry is required")
	}
	if c.OTel.ServiceName == "" {
		return fmt.Errorf("otel.serviceName is required")
	}
	return nil
}
