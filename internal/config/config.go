package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Temporal TemporalConfig `yaml:"temporal"`
	Auth     AuthConfig     `yaml:"auth"`
	ArgoCD   ArgoCDConfig   `yaml:"argocd"`
	OCI      OCIConfig      `yaml:"oci"`
	GitHub   GitHubConfig   `yaml:"github"`
	DocDB    DocumentDBConfig `yaml:"documentdb"`
	OTel     OTelConfig     `yaml:"otel"`
	Policies PoliciesConfig `yaml:"policies"`
	Deploy   DeployDefaults `yaml:"deploymentDefaults"`
}

type ServerConfig struct {
	Addr         string        `yaml:"addr"`
	ReadTimeout  time.Duration `yaml:"readTimeout"`
	WriteTimeout time.Duration `yaml:"writeTimeout"`
}

type TemporalConfig struct {
	HostPort  string `yaml:"hostPort"`
	Namespace string `yaml:"namespace"`
	TaskQueue string `yaml:"taskQueue"`
	TLS       TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	CertPath string `yaml:"certPath"`
	KeyPath  string `yaml:"keyPath"`
	CAPath   string `yaml:"caPath"`
}

type AuthConfig struct {
	OIDC                OIDCConfig `yaml:"oidc"`
	AllowedRepositories []string   `yaml:"allowedRepositories"`
}

type OIDCConfig struct {
	Issuer       string        `yaml:"issuer"`
	Audience     string        `yaml:"audience"`
	JWKSCacheTTL time.Duration `yaml:"jwksCacheTTL"`
}

type ArgoCDConfig struct {
	ServerURL       string `yaml:"serverURL"`
	TokenSecretName string `yaml:"tokenSecretName"`
	Token           string `yaml:"-"` // populated at runtime from Key Vault
	Insecure        bool   `yaml:"insecure"`
}

type OCIConfig struct {
	Registry         string `yaml:"registry"`
	RepositoryPrefix string `yaml:"repositoryPrefix"`
}

type GitHubConfig struct {
	AppID              int64  `yaml:"appID"`
	InstallationID     int64  `yaml:"installationID"`
	PrivateKeyPath     string `yaml:"privateKeyPath"`
	PrivateKeySecretName string `yaml:"privateKeySecretName"`
	Token              string `yaml:"-"` // for development; in prod use GitHub App
}

type DocumentDBConfig struct {
	Endpoint              string `yaml:"endpoint"`
	Database              string `yaml:"database"`
	DeploymentsCollection string `yaml:"deploymentsCollection"`
	EventsCollection      string `yaml:"eventsCollection"`
	ConnectionString      string `yaml:"-"` // populated from env/vault
}

type OTelConfig struct {
	ServiceName      string  `yaml:"serviceName"`
	OTLPEndpoint     string  `yaml:"otlpEndpoint"`
	TracesSampleRate float64 `yaml:"tracesSampleRate"`
	Insecure         bool    `yaml:"insecure"`
}

type PoliciesConfig struct {
	ConfigPath string `yaml:"configPath"`
}

type DeployDefaults struct {
	SyncTimeoutSeconds              int `yaml:"syncTimeoutSeconds"`
	HealthConvergenceTimeoutSeconds int `yaml:"healthConvergenceTimeoutSeconds"`
	WorkflowTimeoutMinutes          int `yaml:"workflowTimeoutMinutes"`
	PollIntervalSeconds             int `yaml:"pollIntervalSeconds"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Expand environment variables in config
	expanded := os.ExpandEnv(string(data))

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

func (c *Config) applyDefaults() {
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
	if c.DocDB.EventsCollection == "" {
		c.DocDB.EventsCollection = "deployment-events"
	}
}

func (c *Config) validate() error {
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
