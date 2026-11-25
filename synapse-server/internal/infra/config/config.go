// Package config provides strongly typed YAML + environment + CLI configuration
// loading, fail-fast validation, and corporate proxy / custom CA runtime setup.
package config

import "time"

// Config is the root configuration tree for synapse-server (zero Redis sections).
type Config struct {
	Server        ServerConfig        `yaml:"server"`
	App           AppConfig           `yaml:"app"`
	Storage       StorageConfig       `yaml:"storage"`
	Secret        SecretConfig        `yaml:"secret"`
	Network       NetworkConfig       `yaml:"network"`
	Runtime       RuntimeConfig       `yaml:"runtime"`
	Paths         PathsConfig         `yaml:"paths"`
	Quota         QuotaConfig         `yaml:"quota"`
	Agent         AgentConfig         `yaml:"agent"`
	Chat          ChatConfig          `yaml:"chat"`
	MCP           MCPConfig           `yaml:"mcp"`
	Files         FilesConfig         `yaml:"files"`
	Task          TaskConfig          `yaml:"task"`
	Automation    AutomationConfig    `yaml:"automation"`
	SuperAgent    SuperAgentConfig    `yaml:"superagent"`
	Memory        MemoryConfig        `yaml:"memory"`
	Provider      ProviderConfig      `yaml:"provider"`
	Cost          CostConfig          `yaml:"cost"`
	Inbox         InboxConfig         `yaml:"inbox"`
	AgentLog      AgentLogConfig      `yaml:"agent_log"`
	Observability ObservabilityConfig `yaml:"observability"`
	Platform      PlatformConfig      `yaml:"platform"`
}

// ServerConfig controls HTTP listener, UI serving, session cookie, and shutdown settings.
type ServerConfig struct {
	Listen              string        `yaml:"listen"`
	ShutdownTimeout     time.Duration `yaml:"shutdown_timeout"`
	UIDir               string        `yaml:"ui_dir"`
	SessionCookieName   string        `yaml:"session_cookie_name"`
	SessionCookieSecure bool          `yaml:"session_cookie_secure"`
	SessionTTL          time.Duration `yaml:"session_ttl"`
	TrustProxyHeaders   bool          `yaml:"trust_proxy_headers"`
}

// AppConfig holds application-level public metadata settings.
type AppConfig struct {
	BaseURL string `yaml:"base_url"`
}

// StorageConfig holds single-database PostgreSQL 17 and migration settings.
type StorageConfig struct {
	AutoMigrate bool           `yaml:"auto_migrate"`
	Postgres    PostgresConfig `yaml:"postgres"`
}

// PostgresConfig defines PostgreSQL 17 connection pool parameters consumed by SA-02.
type PostgresConfig struct {
	DSN                string        `yaml:"dsn"`
	MaxConns           int           `yaml:"max_conns"`
	MinConns           int           `yaml:"min_conns"`
	MaxConnLifetime    time.Duration `yaml:"max_conn_lifetime"`
	MaxConnIdleTime    time.Duration `yaml:"max_conn_idle_time"`
	SlowQueryThreshold time.Duration `yaml:"slow_query_threshold"`
}

// SecretConfig controls the master KEK for per-tenant envelope encryption.
type SecretConfig struct {
	Key     string `yaml:"key"`
	KeyFile string `yaml:"key_file"`
}

// NetworkConfig defines corporate outbound HTTP(S) proxy and enterprise CA bundle paths.
type NetworkConfig struct {
	HTTPProxy    string `yaml:"http_proxy"`
	HTTPSProxy   string `yaml:"https_proxy"`
	NoProxy      string `yaml:"no_proxy"`
	CABundleFile string `yaml:"ca_bundle_file"`
}

// RuntimeConfig configures managed Node.js and npm registries for CLI runtimes.
type RuntimeConfig struct {
	NodeDir        string `yaml:"node_dir"`
	NPMRegistry    string `yaml:"npm_registry"`
	NodeDistMirror string `yaml:"node_dist_mirror"`
}

// PathsConfig specifies the 7 tenant-partitioned local data directories.
type PathsConfig struct {
	Workspaces  string `yaml:"workspaces"`
	Tasks       string `yaml:"tasks"`
	Files       string `yaml:"files"`
	Attachments string `yaml:"attachments"`
	Avatars     string `yaml:"avatars"`
	Branding    string `yaml:"branding"`
	Skills      string `yaml:"skills"`
}

// QuotaConfig defines default per-company resource caps (0 means unlimited).
type QuotaConfig struct {
	MaxUsers        int   `yaml:"max_users"`
	MaxRunningTasks int   `yaml:"max_running_tasks"`
	MonthlyTokens   int64 `yaml:"monthly_tokens"`
}

// AgentConfig configures default agent execution parameters and V1 GA CLI runtimes.
type AgentConfig struct {
	DefaultModel        string       `yaml:"default_model"`
	AllowedTools        string       `yaml:"allowed_tools"`
	SkipPermissions     bool         `yaml:"skip_permissions"`
	SessionFailureLimit int          `yaml:"session_failure_limit"`
	Claude              ClaudeConfig `yaml:"claude"`
	Codex               CodexConfig  `yaml:"codex"`
}

// ClaudeConfig configures the Claude Code CLI adapter and its warm process pool.
type ClaudeConfig struct {
	Command         string `yaml:"command"`
	TimeoutSeconds  int    `yaml:"timeout_seconds"`
	WarmEnabled     bool   `yaml:"warm_enabled"`
	WarmIdleMinutes int    `yaml:"warm_idle_minutes"`
	WarmMax         int    `yaml:"warm_max"`
}

// CodexConfig configures the Codex CLI adapter.
type CodexConfig struct {
	Command        string `yaml:"command"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

// ChatConfig configures interactive chat streaming limits.
type ChatConfig struct {
	SSETimeout time.Duration `yaml:"sse_timeout"`
}

// MCPConfig configures the built-in MCP server gateway.
type MCPConfig struct {
	EnabledInChat bool   `yaml:"enabled_in_chat"`
	SelfURL       string `yaml:"self_url"`
}

// FilesConfig controls work-file size limits and automatic tagging.
type FilesConfig struct {
	UploadMaxMB int  `yaml:"upload_max_mb"`
	MaxMB       int  `yaml:"max_mb"`
	AutoTag     bool `yaml:"auto_tag"`
}

// TaskConfig configures background task execution, scheduler, and watchdog parameters.
type TaskConfig struct {
	AllowedTools     string        `yaml:"allowed_tools"`
	SkipPermissions  bool          `yaml:"skip_permissions"`
	MaxParallel      int           `yaml:"max_parallel"`
	SchedulerSweep   time.Duration `yaml:"scheduler_sweep"`
	WatchdogEnabled  bool          `yaml:"watchdog_enabled"`
	WatchdogInterval time.Duration `yaml:"watchdog_interval"`
	WatchdogStall    time.Duration `yaml:"watchdog_stall"`
}

// AutomationConfig controls cron/one-shot automation scheduler cadence.
type AutomationConfig struct {
	SchedulerEnabled  bool          `yaml:"scheduler_enabled"`
	SchedulerInterval time.Duration `yaml:"scheduler_interval"`
}

// SuperAgentConfig configures the personal SuperAgent orchestrator loop.
type SuperAgentConfig struct {
	Enabled      bool          `yaml:"enabled"`
	TickInterval time.Duration `yaml:"tick_interval"`
	QuietWindow  time.Duration `yaml:"quiet_window"`
	MaxWait      time.Duration `yaml:"max_wait"`
	TeamLimit    int           `yaml:"team_limit"`
	Timezone     string        `yaml:"timezone"`
}

// MemoryConfig configures periodic long-term agent memory summarization.
type MemoryConfig struct {
	Enabled       bool          `yaml:"enabled"`
	Interval      time.Duration `yaml:"interval"`
	MinMessages   int           `yaml:"min_messages"`
	SettleMinutes int           `yaml:"settle_minutes"`
	IdleMinutes   int           `yaml:"idle_minutes"`
	AgentsPerTick int           `yaml:"agents_per_tick"`
	CarryChars    int           `yaml:"carry_chars"`
	Timezone      string        `yaml:"timezone"`
}

// ProviderConfig controls background provider/credential status probes.
type ProviderConfig struct {
	CheckerEnabled     bool          `yaml:"checker_enabled"`
	LocalInterval      time.Duration `yaml:"local_interval"`
	LatestInterval     time.Duration `yaml:"latest_interval"`
	InferInterval      time.Duration `yaml:"infer_interval"`
	CredentialInterval time.Duration `yaml:"credential_interval"`
}

// CostConfig configures the model pricing catalog synchronization source.
type CostConfig struct {
	CatalogURL           string        `yaml:"catalog_url"`
	CatalogCheckInterval time.Duration `yaml:"catalog_check_interval"`
}

// InboxConfig configures IM inbox retention.
type InboxConfig struct {
	RetentionDays int `yaml:"retention_days"`
}

// AgentLogConfig configures agent audit log retention and per-agent cap.
type AgentLogConfig struct {
	RetentionDays int `yaml:"retention_days"`
	MaxPerAgent   int `yaml:"max_per_agent"`
}

// ObservabilityConfig configures structured logging and Prometheus scrape auth.
type ObservabilityConfig struct {
	LogLevel        string `yaml:"log_level"`
	LogFormat       string `yaml:"log_format"`
	LogFile         string `yaml:"log_file"`
	PrometheusToken string `yaml:"prometheus_token"`
}

// PlatformConfig holds reserved commercial platform control-plane settings.
type PlatformConfig struct {
	SuperAdminToken string `yaml:"super_admin_token"`
}

// Default returns a Config populated with production-safe Synapse-OS V1 defaults.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Listen:              ":18080",
			ShutdownTimeout:     30 * time.Second,
			UIDir:               "",
			SessionCookieName:   "SYNAPSE_SESSION",
			SessionCookieSecure: false,
			SessionTTL:          168 * time.Hour,
			TrustProxyHeaders:   true,
		},
		App: AppConfig{
			BaseURL: "",
		},
		Storage: StorageConfig{
			AutoMigrate: true,
			Postgres: PostgresConfig{
				DSN:                "postgres://synapse:synapse@127.0.0.1:5432/synapse?sslmode=disable&TimeZone=UTC",
				MaxConns:           16,
				MinConns:           2,
				MaxConnLifetime:    30 * time.Minute,
				MaxConnIdleTime:    5 * time.Minute,
				SlowQueryThreshold: 200 * time.Millisecond,
			},
		},
		Secret: SecretConfig{
			Key:     "",
			KeyFile: "./data/secret.key",
		},
		Network: NetworkConfig{
			HTTPProxy:    "",
			HTTPSProxy:   "",
			NoProxy:      "localhost,127.0.0.1,postgres",
			CABundleFile: "",
		},
		Runtime: RuntimeConfig{
			NodeDir:        "./data/runtime/node",
			NPMRegistry:    "",
			NodeDistMirror: "https://nodejs.org/dist",
		},
		Paths: PathsConfig{
			Workspaces:  "./data/workspaces",
			Tasks:       "./data/tasks",
			Files:       "./data/files",
			Attachments: "./data/attachments",
			Avatars:     "./data/avatars",
			Branding:    "./data/branding",
			Skills:      "./data/skills",
		},
		Quota: QuotaConfig{
			MaxUsers:        0,
			MaxRunningTasks: 0,
			MonthlyTokens:   0,
		},
		Agent: AgentConfig{
			DefaultModel:        "",
			AllowedTools:        "Read,Glob,Grep,LS,Bash",
			SkipPermissions:     false,
			SessionFailureLimit: 10,
			Claude: ClaudeConfig{
				Command:         "claude",
				TimeoutSeconds:  600,
				WarmEnabled:     true,
				WarmIdleMinutes: 10,
				WarmMax:         20,
			},
			Codex: CodexConfig{
				Command:        "codex",
				TimeoutSeconds: 600,
			},
		},
		Chat: ChatConfig{
			SSETimeout: 10 * time.Minute,
		},
		MCP: MCPConfig{
			EnabledInChat: true,
			SelfURL:       "",
		},
		Files: FilesConfig{
			UploadMaxMB: 50,
			MaxMB:       200,
			AutoTag:     true,
		},
		Task: TaskConfig{
			AllowedTools:     "Read,Glob,Grep,LS,Edit,Write,Bash",
			SkipPermissions:  false,
			MaxParallel:      50,
			SchedulerSweep:   30 * time.Second,
			WatchdogEnabled:  true,
			WatchdogInterval: 30 * time.Second,
			WatchdogStall:    120 * time.Second,
		},
		Automation: AutomationConfig{
			SchedulerEnabled:  true,
			SchedulerInterval: 30 * time.Second,
		},
		SuperAgent: SuperAgentConfig{
			Enabled:      true,
			TickInterval: 3 * time.Second,
			QuietWindow:  10 * time.Second,
			MaxWait:      60 * time.Second,
			TeamLimit:    6,
			Timezone:     "Asia/Shanghai",
		},
		Memory: MemoryConfig{
			Enabled:       true,
			Interval:      5 * time.Minute,
			MinMessages:   10,
			SettleMinutes: 2,
			IdleMinutes:   30,
			AgentsPerTick: 3,
			CarryChars:    60000,
			Timezone:      "Asia/Shanghai",
		},
		Provider: ProviderConfig{
			CheckerEnabled:     true,
			LocalInterval:      3 * time.Second,
			LatestInterval:     time.Hour,
			InferInterval:      time.Hour,
			CredentialInterval: 10 * time.Minute,
		},
		Cost: CostConfig{
			CatalogURL:           "https://models.dev/api.json",
			CatalogCheckInterval: 10 * time.Minute,
		},
		Inbox: InboxConfig{
			RetentionDays: 30,
		},
		AgentLog: AgentLogConfig{
			RetentionDays: 30,
			MaxPerAgent:   5000,
		},
		Observability: ObservabilityConfig{
			LogLevel:        "info",
			LogFormat:       "text",
			LogFile:         "",
			PrometheusToken: "",
		},
		Platform: PlatformConfig{
			SuperAdminToken: "",
		},
	}
}
