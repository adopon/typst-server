// Package config loads ponado-typst settings from the environment.
package config

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

const (
	defaultPort   = "3100"
	defaultBodyMB = 25
)

// Config is the fully resolved service configuration.
type Config struct {
	// Port is the TCP port to listen on (PORT, default 3100).
	Port string
	// TypstBin is the typst binary name or path (TYPST_BIN, default "typst").
	TypstBin string
	// MaxConcurrency caps parallel compiles; <= 0 means unlimited
	// (MAX_CONCURRENCY, default NumCPU).
	MaxConcurrency int
	// MaxBodyBytes caps request bodies (MAX_BODY_MB, default 25MB).
	MaxBodyBytes int64
}

// FromEnv reads the configuration, returning an error for malformed values
// instead of silently falling back to defaults.
func FromEnv() (Config, error) {
	cfg := Config{
		Port:           defaultPort,
		TypstBin:       "typst",
		MaxConcurrency: runtime.NumCPU(),
		MaxBodyBytes:   defaultBodyMB << 20,
	}

	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("TYPST_BIN"); v != "" {
		cfg.TypstBin = v
	}

	if v := strings.TrimSpace(os.Getenv("MAX_CONCURRENCY")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid MAX_CONCURRENCY %q: %w", v, err)
		}
		cfg.MaxConcurrency = n
	}

	if v := strings.TrimSpace(os.Getenv("MAX_BODY_MB")); v != "" {
		mb, err := strconv.Atoi(v)
		if err != nil || mb <= 0 {
			return Config{}, fmt.Errorf("invalid MAX_BODY_MB %q: want a positive integer", v)
		}
		cfg.MaxBodyBytes = int64(mb) << 20
	}

	return cfg, nil
}
