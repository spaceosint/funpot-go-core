package config

import (
	"fmt"
	"os"
	"strconv"
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
