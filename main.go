package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func main() {
	configPath := flag.String("f", "skew.yaml", "path to the YAML config file")
	only := flag.String("only", "", "comma-separated service names to start")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fatalf("%v", err)
	}

	services, err := selectServices(cfg.Services, *only)
	if err != nil {
		fatalf("%v", err)
	}

	model := newModel(services, *configPath, *only)
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fatalf("run TUI: %v", err)
	}
}

func selectServices(services []Service, selection string) ([]Service, error) {
	if strings.TrimSpace(selection) == "" {
		return services, nil
	}
	wanted := make(map[string]bool)
	for _, name := range strings.Split(selection, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			wanted[name] = true
		}
	}
	selected := make([]Service, 0, len(wanted))
	for _, service := range services {
		if wanted[service.Name] {
			selected = append(selected, service)
			delete(wanted, service.Name)
		}
	}
	if len(wanted) != 0 {
		names := make([]string, 0, len(wanted))
		for name := range wanted {
			names = append(names, name)
		}
		return nil, fmt.Errorf("unknown service(s): %s", strings.Join(names, ", "))
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("-only did not select any services")
	}
	return selected, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
