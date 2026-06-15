// SPDX-License-Identifier: Apache-2.0

// Package config loads threadwatch's runtime configuration from environment
// variables and a YAML file describing the threads to watch.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the resolved, validated configuration for one threadwatch process.
type Config struct {
	ListenAddr   string        // LISTEN_ADDR, default ":8080"
	DatabasePath string        // DATABASE_PATH, default "/data/threadwatch.db"
	PollInterval time.Duration // POLL_INTERVAL, default 15m
	ThreadsPath  string        // THREADS_CONFIG_PATH, default "/etc/threadwatch/threads.yaml"
	GitHubToken  string        // GITHUB_TOKEN, empty allowed (lower rate limit only)
	RefreshToken string        // REFRESH_TOKEN, empty disables /api/threads/refresh
	Threads      []Thread      // parsed from the YAML at ThreadsPath
}

// Thread is the YAML-declared shape of one watched thread.
type Thread struct {
	Label  string `yaml:"label"`
	Owner  string `yaml:"owner"`
	Repo   string `yaml:"repo"`
	Number int    `yaml:"number"`
}

// Load resolves env + reads the threads file. The threads file is optional:
// an empty or missing file produces a Config with no Threads, which is the
// "fresh deploy before anyone added URLs" case rather than an error.
func Load() (Config, error) {
	c := Config{
		ListenAddr:   envDefault("LISTEN_ADDR", ":8080"),
		DatabasePath: envDefault("DATABASE_PATH", "/data/threadwatch.db"),
		ThreadsPath:  envDefault("THREADS_CONFIG_PATH", "/etc/threadwatch/threads.yaml"),
		GitHubToken:  os.Getenv("GITHUB_TOKEN"),
		RefreshToken: os.Getenv("REFRESH_TOKEN"),
	}

	pi := envDefault("POLL_INTERVAL", "15m")
	d, err := time.ParseDuration(pi)
	if err != nil {
		return Config{}, fmt.Errorf("POLL_INTERVAL %q: %w", pi, err)
	}
	if d < 30*time.Second {
		return Config{}, fmt.Errorf("POLL_INTERVAL must be >= 30s, got %s", d)
	}
	c.PollInterval = d

	threads, err := loadThreadsFile(c.ThreadsPath)
	if err != nil {
		return Config{}, err
	}
	c.Threads = threads
	return c, nil
}

func loadThreadsFile(path string) ([]Thread, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read threads config %q: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}

	var doc struct {
		Threads []Thread `yaml:"threads"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse threads config %q: %w", path, err)
	}

	for i, t := range doc.Threads {
		if t.Owner == "" || t.Repo == "" || t.Number <= 0 {
			return nil, fmt.Errorf("threads[%d]: owner, repo, and number are required", i)
		}
		if t.Label == "" {
			doc.Threads[i].Label = fmt.Sprintf("%s/%s#%d", t.Owner, t.Repo, t.Number)
		}
	}
	return doc.Threads, nil
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
