package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Services []Service `yaml:"services"`
}

type Service struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Cwd     string            `yaml:"cwd"`
	Env     map[string]string `yaml:"env"`
	Watch   WatchConfig       `yaml:"watch"`
}

type WatchConfig struct {
	Paths    []string      `yaml:"paths"`
	Ignore   []string      `yaml:"ignore"`
	Debounce string        `yaml:"debounce"`
	Delay    time.Duration `yaml:"-"`
}

func loadConfig(path string) (Config, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	file, err := os.Open(absolutePath)
	if err != nil {
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if len(cfg.Services) == 0 {
		return Config{}, fmt.Errorf("config %q must define at least one service", path)
	}

	configDir := filepath.Dir(absolutePath)
	names := make(map[string]bool)
	for i := range cfg.Services {
		service := &cfg.Services[i]
		service.Name = strings.TrimSpace(service.Name)
		if service.Name == "" {
			return Config{}, fmt.Errorf("services[%d] is missing a name", i)
		}
		if names[service.Name] {
			return Config{}, fmt.Errorf("duplicate service name %q", service.Name)
		}
		names[service.Name] = true
		service.Command = strings.TrimSpace(service.Command)
		if service.Command == "" {
			return Config{}, fmt.Errorf("service %q is missing a command", service.Name)
		}
		if service.Cwd == "" {
			service.Cwd = configDir
		} else if !filepath.IsAbs(service.Cwd) {
			service.Cwd = filepath.Join(configDir, service.Cwd)
		}
		service.Cwd, err = filepath.Abs(service.Cwd)
		if err != nil {
			return Config{}, fmt.Errorf("resolve cwd for service %q: %w", service.Name, err)
		}
		if service.Watch.Debounce == "" {
			service.Watch.Delay = 250 * time.Millisecond
		} else {
			service.Watch.Delay, err = time.ParseDuration(service.Watch.Debounce)
			if err != nil || service.Watch.Delay <= 0 {
				return Config{}, fmt.Errorf("service %q has invalid watch debounce %q (use a positive duration like 250ms)", service.Name, service.Watch.Debounce)
			}
		}
		for j, path := range service.Watch.Paths {
			path = strings.TrimSpace(path)
			if path == "" {
				return Config{}, fmt.Errorf("service %q has an empty watch path", service.Name)
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(service.Cwd, path)
			}
			service.Watch.Paths[j] = filepath.Clean(path)
		}
	}
	return cfg, nil
}
