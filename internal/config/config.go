package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config aggregates all service configuration domains.
type Config struct {
	Environment string
	Server      ServerConfig
	Logging     LoggingConfig
	Telemetry   TelemetryConfig
	Sentry      SentryConfig
	Auth        AuthConfig
	Database    DatabaseConfig
	Features    FeatureConfig
	Client      ClientConfig
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Address         string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

// LoggingConfig controls logger behavior.
type LoggingConfig struct {
	Level string
}

// TelemetryConfig controls tracing/metrics exporters.
type TelemetryConfig struct {
	ServiceName    string
	MetricsEnabled bool
}

// SentryConfig wires Sentry error tracking.
type SentryConfig struct {
	DSN         string
	Environment string
	SampleRate  float64
	Debug       bool
}

// AuthConfig controls Telegram authentication and JWT issuance.
type AuthConfig struct {
	BotToken string
	JWT      JWTConfig
}

// DatabaseConfig controls PostgreSQL connectivity.
type DatabaseConfig struct {
	Enabled         bool
	Host            string
	Port            int
	Name            string
	User            string
	Password        string
	SSLMode         string
	MaxOpenConns    int
	MinOpenConns    int
	MaxIdleConns    int
	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration
	ConnectTimeout  time.Duration
	HealthcheckPing time.Duration
}

// DSN builds a PostgreSQL connection string from database fields.
func (d DatabaseConfig) DSN() string {
	if d.Host == "" || d.Port <= 0 || d.Name == "" || d.User == "" {
		return ""
	}

	connURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(d.User, d.Password),
		Host:   fmt.Sprintf("%s:%d", d.Host, d.Port),
		Path:   d.Name,
	}

	q := connURL.Query()
	q.Set("sslmode", d.SSLMode)
	connURL.RawQuery = q.Encode()

	return connURL.String()
}

// JWTConfig holds settings for token issuance.
type JWTConfig struct {
	Secret string
	TTL    time.Duration
}

// FeatureConfig describes dynamic feature flag exposure.
type FeatureConfig struct {
	Flags map[string]bool
}

// ClientConfig is returned by /api/config for Mini App runtime behavior.
type ClientConfig struct {
	StarsRate  float64
	MinViewers int
	Currencies []string
	VotePerMin int
}

// Load reads configuration from the environment, applying defaults and .env overrides.
func Load() (Config, error) {
	_ = godotenv.Load()

	readTimeout, err := getDuration("FUNPOT_SERVER_READ_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}

	writeTimeout, err := getDuration("FUNPOT_SERVER_WRITE_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := getDuration("FUNPOT_SERVER_SHUTDOWN_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}

	metricsEnabled, err := getBool("FUNPOT_TELEMETRY_METRICS_ENABLED", true)
	if err != nil {
		return Config{}, err
	}

	sampleRate, err := getFloat("FUNPOT_SENTRY_TRACES_SAMPLE_RATE", 0.0)
	if err != nil {
		return Config{}, err
	}

	sentryDebug, err := getBool("FUNPOT_SENTRY_DEBUG", false)
	if err != nil {
		return Config{}, err
	}

	jwtTTL, err := getDuration("FUNPOT_AUTH_JWT_TTL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}

	databaseEnabled, err := getBool("FUNPOT_DATABASE_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	maxOpenConns, err := getInt("FUNPOT_DATABASE_MAX_OPEN_CONNS", 10)
	if err != nil {
		return Config{}, err
	}

	minOpenConns, err := getInt("FUNPOT_DATABASE_MIN_OPEN_CONNS", 1)
	if err != nil {
		return Config{}, err
	}

	connectTimeout, err := getDuration("FUNPOT_DATABASE_CONNECT_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}

	healthcheckPing, err := getDuration("FUNPOT_DATABASE_HEALTHCHECK_TIMEOUT", 1*time.Second)
	if err != nil {
		return Config{}, err
	}

	featureFlags, err := getFeatureFlags("FUNPOT_FEATURE_FLAGS")
	if err != nil {
		return Config{}, err
	}

	starsRate, err := getFloat("FUNPOT_CLIENT_STARS_RATE", 1)
	if err != nil {
		return Config{}, err
	}

	minViewers, err := getInt("FUNPOT_CLIENT_MIN_VIEWERS", 100)
	if err != nil {
		return Config{}, err
	}

	votePerMin, err := getInt("FUNPOT_CLIENT_LIMIT_VOTE_PER_MIN", 30)
	if err != nil {
		return Config{}, err
	}

	currencies := getCSVStrings("FUNPOT_CLIENT_CURRENCIES", []string{"INT"})

	maxIdleConns, err := getInt("FUNPOT_DATABASE_MAX_IDLE_CONNS", 5)
	if err != nil {
		return Config{}, err
	}

	connMaxIdleTime, err := getDuration("FUNPOT_DATABASE_CONN_MAX_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}

	connMaxLifetime, err := getDuration("FUNPOT_DATABASE_CONN_MAX_LIFETIME", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}

	databasePort, err := getInt("FUNPOT_DATABASE_PORT", 5432)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Environment: getString("FUNPOT_ENV", "development"),
		Server: ServerConfig{
			Address:         getString("FUNPOT_SERVER_ADDRESS", ":8080"),
			ReadTimeout:     readTimeout,
			WriteTimeout:    writeTimeout,
			ShutdownTimeout: shutdownTimeout,
		},
		Logging: LoggingConfig{
			Level: getString("FUNPOT_LOG_LEVEL", "info"),
		},
		Telemetry: TelemetryConfig{
			ServiceName:    getString("FUNPOT_TELEMETRY_SERVICE_NAME", "funpot-core"),
			MetricsEnabled: metricsEnabled,
		},
		Sentry: SentryConfig{
			DSN:         os.Getenv("FUNPOT_SENTRY_DSN"),
			Environment: getString("FUNPOT_SENTRY_ENVIRONMENT", "development"),
			SampleRate:  sampleRate,
			Debug:       sentryDebug,
		},
		Auth: AuthConfig{
			BotToken: os.Getenv("FUNPOT_AUTH_TELEGRAM_BOT_TOKEN"),
			JWT: JWTConfig{
				Secret: getString("FUNPOT_AUTH_JWT_SECRET", "dev-secret"),
				TTL:    jwtTTL,
			},
		},
		Database: DatabaseConfig{
			Enabled:         databaseEnabled,
			Host:            os.Getenv("FUNPOT_DATABASE_HOST"),
			Port:            databasePort,
			Name:            os.Getenv("FUNPOT_DATABASE_NAME"),
			User:            os.Getenv("FUNPOT_DATABASE_USER"),
			Password:        os.Getenv("FUNPOT_DATABASE_PASSWORD"),
			SSLMode:         getString("FUNPOT_DATABASE_SSLMODE", "disable"),
			MaxOpenConns:    maxOpenConns,
			MinOpenConns:    minOpenConns,
			MaxIdleConns:    maxIdleConns,
			ConnMaxIdleTime: connMaxIdleTime,
			ConnMaxLifetime: connMaxLifetime,
			ConnectTimeout:  connectTimeout,
			HealthcheckPing: healthcheckPing,
		},
		Features: FeatureConfig{
			Flags: featureFlags,
		},
		Client: ClientConfig{
			StarsRate:  starsRate,
			MinViewers: minViewers,
			Currencies: currencies,
			VotePerMin: votePerMin,
		},
	}

	if cfg.Database.Enabled {
		if cfg.Database.Host == "" || cfg.Database.Name == "" || cfg.Database.User == "" {
			return Config{}, fmt.Errorf("FUNPOT_DATABASE_HOST, FUNPOT_DATABASE_NAME and FUNPOT_DATABASE_USER are required when FUNPOT_DATABASE_ENABLED=true")
		}
		if cfg.Database.Port < 1 || cfg.Database.Port > 65535 {
			return Config{}, fmt.Errorf("FUNPOT_DATABASE_PORT must be between 1 and 65535")
		}
	}

	if cfg.Database.MinOpenConns < 0 || cfg.Database.MaxOpenConns < 1 || cfg.Database.MinOpenConns > cfg.Database.MaxOpenConns {
		return Config{}, fmt.Errorf("invalid database pool bounds: min=%d max=%d", cfg.Database.MinOpenConns, cfg.Database.MaxOpenConns)
	}

	return cfg, nil
}

func getString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getBool(key string, fallback bool) (bool, error) {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return false, fmt.Errorf("invalid boolean for %s: %w", key, err)
		}
		return parsed, nil
	}
	return fallback, nil
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	if value := os.Getenv(key); value != "" {
		dur, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("invalid duration for %s: %w", key, err)
		}
		return dur, nil
	}
	return fallback, nil
}

func getFloat(key string, fallback float64) (float64, error) {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid float for %s: %w", key, err)
		}
		return parsed, nil
	}
	return fallback, nil
}

func getInt(key string, fallback int) (int, error) {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("invalid int for %s: %w", key, err)
		}
		return parsed, nil
	}
	return fallback, nil
}

func getFeatureFlags(key string) (map[string]bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return map[string]bool{}, nil
	}

	flags := make(map[string]bool)
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid feature flag pair: %s", entry)
		}
		key := strings.TrimSpace(parts[0])
		rawValue := strings.TrimSpace(parts[1])
		if key == "" {
			return nil, fmt.Errorf("feature flag key missing in pair: %s", entry)
		}
		enabled, err := strconv.ParseBool(rawValue)
		if err != nil {
			return nil, fmt.Errorf("invalid feature flag value for %s: %w", key, err)
		}
		flags[key] = enabled
	}
	return flags, nil
}

func getCSVStrings(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	rawItems := strings.Split(value, ",")
	items := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			items = append(items, trimmed)
		}
	}
	if len(items) == 0 {
		return fallback
	}
	return items
}
