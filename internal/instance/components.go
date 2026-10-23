package instance

import (
	"fmt"
	"regexp"
	"strings"

	"webdock/internal/render"
)

const (
	ComponentNginx    = "nginx"
	ComponentPHP      = "php"
	ComponentRedis    = "redis"
	ComponentMySQL    = "mysql"
	ComponentPostgres = "postgres"
)

type Component struct {
	Name    string
	Enabled bool
	Setting string
	Version string
	Image   string
}

type componentSpec struct {
	name           string
	env            string
	defaultVer     string
	dataVersionKey string
	image          func(string) string
	envKeys        []string
	paths          []string
}

var componentSpecs = []componentSpec{
	{
		name:       ComponentNginx,
		env:        "ENABLE_NGINX",
		defaultVer: "1.26",
		image:      func(version string) string { return "hicasper/webdock:nginx-" + version },
		envKeys:    []string{"ENABLE_NGINX", "NGINX_UI_APP_JWT_SECRET", "NGINX_UI_CRYPTO_SECRET"},
		paths:      []string{"config/nginx", "data/nginx-ui"},
	},
	{
		name:       ComponentPHP,
		env:        "ENABLE_PHP",
		defaultVer: "8.4",
		image:      func(version string) string { return "hicasper/webdock:php-" + version },
		envKeys:    []string{"ENABLE_PHP"},
		paths:      []string{"config/php"},
	},
	{
		name:           ComponentRedis,
		env:            "ENABLE_REDIS",
		defaultVer:     "8",
		dataVersionKey: "REDIS",
		image:          func(version string) string { return "valkey/valkey:" + version },
		envKeys:        []string{"ENABLE_REDIS", "REDIS_PASSWORD", "REDIS_MAXMEMORY", "REDIS_BRIDGE_NETWORKS"},
		paths:          []string{"config/redis", "data/redis"},
	},
	{
		name:           ComponentMySQL,
		env:            "ENABLE_MYSQL",
		defaultVer:     "8.4",
		dataVersionKey: "MYSQL",
		image:          func(version string) string { return "mysql:" + version },
		envKeys:        []string{"ENABLE_MYSQL", "MYSQL_ROOT_PASSWORD", "MYSQL_PASSWORD"},
		paths:          []string{"config/mysql", "data/mysql"},
	},
	{
		name:           ComponentPostgres,
		env:            "ENABLE_POSTGRES",
		defaultVer:     "16",
		dataVersionKey: "POSTGRES",
		image: func(version string) string {
			return "tensorchord/vchord-suite:pg" + version + "-latest"
		},
		envKeys: []string{"ENABLE_POSTGRES", "POSTGRES_PASSWORD"},
		paths:   []string{"data/postgres"},
	},
}

var componentVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func ComponentNames() []string {
	names := make([]string, 0, len(componentSpecs))
	for _, spec := range componentSpecs {
		names = append(names, spec.name)
	}
	return names
}

func ResolveComponent(name, setting string) (Component, error) {
	spec, ok := findComponentSpec(name)
	if !ok {
		return Component{}, fmt.Errorf("unknown component %q", name)
	}
	setting = strings.TrimSpace(setting)
	if setting == "" {
		return Component{}, fmt.Errorf("%s setting is empty", name)
	}
	switch strings.ToLower(setting) {
	case "false":
		return Component{Name: name, Setting: setting}, nil
	case "true":
		version := spec.defaultVer
		return Component{Name: name, Enabled: true, Setting: setting, Version: version, Image: spec.image(version)}, nil
	}
	if !componentVersionPattern.MatchString(setting) {
		return Component{}, fmt.Errorf("invalid %s version %q", name, setting)
	}
	return Component{Name: name, Enabled: true, Setting: setting, Version: setting, Image: spec.image(setting)}, nil
}

func ParseComponents(values Values) ([]Component, error) {
	components := make([]Component, 0, len(componentSpecs))
	enabled := make(map[string]bool, len(componentSpecs))
	for _, spec := range componentSpecs {
		raw, ok := values[spec.env]
		if !ok || strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("webdock.env is missing %s", spec.env)
		}
		component, err := ResolveComponent(spec.name, raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", spec.env, err)
		}
		components = append(components, component)
		enabled[component.Name] = component.Enabled
	}
	if enabled[ComponentPHP] && !enabled[ComponentNginx] {
		return nil, fmt.Errorf("php requires nginx to be enabled")
	}
	return components, nil
}

func ComponentEnvKeys(name string) []string {
	spec, ok := findComponentSpec(name)
	if !ok {
		return nil
	}
	return append([]string(nil), spec.envKeys...)
}

func ComponentEnvKey(name string) (string, bool) {
	spec, ok := findComponentSpec(name)
	if !ok || len(spec.envKeys) == 0 {
		return "", false
	}
	return spec.envKeys[0], true
}

func ComponentPaths(name string) []string {
	spec, ok := findComponentSpec(name)
	if !ok {
		return nil
	}
	return append([]string(nil), spec.paths...)
}

func MergeComponentEnvironment(archive, current Values, keepCurrent []string) Values {
	merged := make(Values, len(archive))
	for key, value := range archive {
		merged[key] = value
	}
	for _, name := range keepCurrent {
		for _, key := range ComponentEnvKeys(name) {
			if value, ok := current[key]; ok {
				merged[key] = value
			}
		}
	}
	return merged
}

func (c Config) EnabledComponents() []string {
	result := make([]string, 0, len(c.Components))
	for _, component := range c.Components {
		if component.Enabled {
			result = append(result, component.Name)
		}
	}
	return result
}

func (c Config) Component(name string) (Component, bool) {
	for _, component := range c.Components {
		if component.Name == name {
			return component, true
		}
	}
	return Component{}, false
}

func (c Config) ComposeData() render.ComposeData {
	images := make(map[string]string, len(componentSpecs))
	for _, spec := range componentSpecs {
		component, ok := c.Component(spec.name)
		if ok && component.Enabled {
			images[spec.name] = component.Image
			continue
		}
		images[spec.name] = spec.image(spec.defaultVer)
	}
	return render.ComposeData{
		NginxImage:    images[ComponentNginx],
		PHPImage:      images[ComponentPHP],
		RedisImage:    images[ComponentRedis],
		MySQLImage:    images[ComponentMySQL],
		PostgresImage: images[ComponentPostgres],
	}
}

func SetComponentState(root, name, setting string) (Config, error) {
	spec, ok := findComponentSpec(name)
	if !ok {
		return Config{}, fmt.Errorf("unknown component %q", name)
	}
	if _, err := ResolveComponent(name, setting); err != nil {
		return Config{}, err
	}
	cfg, err := Load(root)
	if err != nil {
		return Config{}, err
	}
	cfg.Values[spec.env] = strings.TrimSpace(setting)
	components, err := ParseComponents(cfg.Values)
	if err != nil {
		return Config{}, err
	}
	cfg.Components = components
	if err := renderEnvFile(cfg.EnvFile(), cfg.Values); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func renderEnvFile(path string, values Values) error {
	return render.WriteFile(path, RenderEnv(values), 0o600)
}

func findComponentSpec(name string) (componentSpec, bool) {
	for _, spec := range componentSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return componentSpec{}, false
}
