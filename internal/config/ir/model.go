package ir

type Config struct {
	Backends         map[string]BackendConfig `mapstructure:"backends"`
	Buckets          []BucketConfig           `mapstructure:"buckets"`
	Features         map[string]bool          `mapstructure:"features,omitempty"`
	CredentialsStore string                   `mapstructure:"credentials_store,omitempty"`
	Server           *ServerConfig            `mapstructure:"server,omitempty"`
	Auth             *AuthConfig              `mapstructure:"auth,omitempty"`
	VirtualHosts     *VirtualHostConfig       `mapstructure:"virtual_hosts,omitempty"`
}

type ServerConfig struct {
	// All fields support both numeric and unit-based formats
	ReadTimeout    any `mapstructure:"read_timeout,omitempty"`     // "30s", "30", default: 15s
	WriteTimeout   any `mapstructure:"write_timeout,omitempty"`    // "30s", "30", default: 15s
	IdleTimeout    any `mapstructure:"idle_timeout,omitempty"`     // "60s", "60", default: 60s
	MaxBodySize    any `mapstructure:"max_body_size,omitempty"`    // "4GB", "4GiB", "4294967296", default: 4GB
	RouteCacheSize any `mapstructure:"route_cache_size,omitempty"` // "1k", "1000", default: 1000
}

type AuthConfig struct {
	DefaultRegion   string `mapstructure:"default_region,omitempty"`    // Default: us-east-1
	ClockSkewLeeway string `mapstructure:"clock_skew_leeway,omitempty"` // Default: "15m" (Go duration format)
}

type VirtualHostConfig struct {
	Hosts []any `mapstructure:"hosts,omitempty"` // List of hosts: string or map[string]string for bucket mapping
}

type BackendConfig struct {
	Endpoint          string             `mapstructure:"endpoint,omitempty"`
	Region            string             `mapstructure:"region,omitempty"`
	Bucket            string             `mapstructure:"bucket"`
	Prefix            string             `mapstructure:"prefix,omitempty"`
	Timeout           string             `mapstructure:"timeout,omitempty"`
	Retries           int                `mapstructure:"retries,omitempty"`
	UseFIPS           bool               `mapstructure:"use_fips,omitempty"`
	UseGlobalEndpoint bool               `mapstructure:"use_global_endpoint,omitempty"`
	UseDualStack      bool               `mapstructure:"use_dual_stack,omitempty"`
	Accelerate        bool               `mapstructure:"accelerate,omitempty"`
	Credentials       *CredentialsConfig `mapstructure:"credentials,omitempty"`
}

type CredentialsConfig struct {
	Type       string                 `mapstructure:"type"`
	Path       string                 `mapstructure:"path,omitempty"`
	SecretName string                 `mapstructure:"secret_name,omitempty"`
	Region     string                 `mapstructure:"region,omitempty"`
	AssumeRole *CredentialsAssumeRole `mapstructure:"assume_role,omitempty"`
	// Inline credentials (only used when type is "inline")
	AccessKeyID     string `mapstructure:"access_key_id,omitempty"`
	SecretAccessKey string `mapstructure:"secret_access_key,omitempty"`
	SessionToken    string `mapstructure:"session_token,omitempty"`
}

type CredentialsAssumeRole struct {
	RoleARN     string `mapstructure:"role_arn"`
	SessionName string `mapstructure:"session_name,omitempty"`
	Duration    string `mapstructure:"duration,omitempty"`
}

type BucketConfig struct {
	Name   string        `mapstructure:"name"`
	Routes []RouteConfig `mapstructure:"routes"`
}

type RouteConfig struct {
	Path     string        `mapstructure:"path"`
	Backend  string        `mapstructure:"backend"`
	Rewrites []RewriteRule `mapstructure:"rewrite,omitempty"`
}

type RewriteRule struct {
	Pattern string `mapstructure:"pattern,omitempty"`
	Result  string `mapstructure:"result"`
}
