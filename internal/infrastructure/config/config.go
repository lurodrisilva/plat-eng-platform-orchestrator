package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// expandEnv expands ${VAR} and ${VAR:-default} references in s. Unlike
// os.ExpandEnv it honors the shell-style `:-default` fallback the config file
// relies on: an unset OR empty VAR yields the default. Plain $VAR is also
// expanded. This is the substitution the config.yaml and docs advertise;
// os.ExpandEnv alone treats `VAR:-default` as a single (missing) variable name
// and silently produces an empty value.
func expandEnv(s string) string {
	return os.Expand(s, func(key string) string {
		name, def := key, ""
		if i := strings.Index(key, ":-"); i >= 0 {
			name, def = key[:i], key[i+2:]
		}
		if v := os.Getenv(name); v != "" {
			return v
		}
		return def
	})
}

// Config holds all application configuration.
type Config struct {
	Server   Server   `yaml:"server"`
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
	// AppNamespace is the namespace the ArgoCD Application objects live in.
	AppNamespace string `yaml:"appNamespace"`
	// Insecure skips TLS verification when reaching argocd-server. In-cluster
	// argocd-server runs secure with a self-signed cert; a same-cluster client
	// sets this true. Hardening follow-up: trust the argocd CA instead.
	Insecure bool `yaml:"insecure"`
}

type OCI struct {
	Registry         string `yaml:"registry"`
	RepositoryPrefix string `yaml:"repositoryPrefix"`
	// AppChartRepository is where scaffolded applications publish their umbrella
	// charts, without the chart name — e.g. ghcr.io/lurodrisilva/helm-charts.
	//
	// Not Registry: that one is where the orchestrator PUBLISHES the composed
	// chart it hands to ArgoCD (ACR, pulled by workload identity). This is where
	// it READS an app's own release output from, which is public GHCR and needs
	// no credential. Empty degrades GET /apps/{name} to repository existence.
	AppChartRepository string `yaml:"appChartRepository"`
}

type GitHub struct {
	Token string `yaml:"-"`
	// GitHub App for the scaffold-new-app dispatch (Phase F). Optional: when
	// AppID/PrivateKey are unset the scaffolder is nil and POST /api/v1/apps
	// answers 503. AppID/InstallationID come from the config file; PrivateKey is
	// a secret populated from env in cmd/server/main.go.
	AppID           int64  `yaml:"appId"`
	InstallationID  int64  `yaml:"installationId"`
	PrivateKey      string `yaml:"-"` // populated from env
	ScaffolderOwner string `yaml:"scaffolderOwner"`
	ScaffolderRepo  string `yaml:"scaffolderRepo"`
	WorkflowFile    string `yaml:"workflowFile"`
	WorkflowRef     string `yaml:"workflowRef"`
	NewRepoOwner    string `yaml:"newRepoOwner"`
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

	expanded := expandEnv(string(data))

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
	if c.ArgoCD.AppNamespace == "" {
		c.ArgoCD.AppNamespace = "argocd"
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
	if c.GitHub.WorkflowRef == "" {
		// net-hexagonal (the scaffolder repo) defaults to master; the workflow
		// file is dispatched from that ref.
		c.GitHub.WorkflowRef = "master"
	}
	if c.GitHub.WorkflowFile == "" {
		c.GitHub.WorkflowFile = "scaffold-new-app.yml"
	}
}

func validate(c *Config) error {
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
