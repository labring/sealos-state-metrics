package registryproxy

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
)

const (
	defaultRegistryScheme    = "http"
	defaultRegistryPort      = 5000
	defaultManifestMediaType = "application/vnd.docker.distribution.manifest.v2+json"
	defaultManifestRepo      = "library/busybox"
	defaultManifestReference = "latest"
)

type registryTarget struct {
	Scheme string
	Host   string
	Port   int
}

type monitoredRegistry struct {
	endpoint             string
	info                 string
	target               registryTarget
	repository           string
	reference            string
	manifestAcceptHeader string
	headers              map[string]string
}

type monitoredRegistryConfig struct {
	Endpoint             string            `mapstructure:"endpoint"`
	Info                 string            `mapstructure:"info"`
	Scheme               string            `mapstructure:"scheme"`
	Repository           string            `mapstructure:"repository"`
	Reference            string            `mapstructure:"reference"`
	ManifestAcceptHeader string            `mapstructure:"manifestAcceptHeader"`
	Headers              map[string]string `mapstructure:"headers"`
}

type runtimeConfig struct {
	registries    []monitoredRegistry
	checkTimeout  time.Duration
	checkInterval time.Duration
}

func newRuntimeConfig(cfg *Config) (*runtimeConfig, error) {
	registryItems := cfg.Registries
	if len(cfg.RegistriesEnv) > 0 {
		registryItems = make([]any, 0, len(cfg.RegistriesEnv))
		for _, value := range cfg.RegistriesEnv {
			registryItems = append(registryItems, value)
		}
	}

	registries, err := parseMonitoredRegistries(registryItems)
	if err != nil {
		return nil, err
	}

	return &runtimeConfig{
		registries:    registries,
		checkTimeout:  cfg.CheckTimeout,
		checkInterval: cfg.CheckInterval,
	}, nil
}

func parseMonitoredRegistries(values []any) ([]monitoredRegistry, error) {
	registries := make([]monitoredRegistry, 0, len(values))

	for _, value := range values {
		registry, err := parseMonitoredRegistry(value)
		if err != nil {
			return nil, err
		}

		if registry.endpoint == "" {
			continue
		}

		registries = append(registries, registry)
	}

	return registries, nil
}

func parseMonitoredRegistry(value any) (monitoredRegistry, error) {
	switch v := value.(type) {
	case string:
		return parseMonitoredRegistryString(v)
	case map[string]any:
		return parseMonitoredRegistryMap(v)
	default:
		return monitoredRegistry{}, fmt.Errorf("invalid registry proxy entry type %T", value)
	}
}

func parseMonitoredRegistryString(value string) (monitoredRegistry, error) {
	endpoint := strings.TrimSpace(value)
	if endpoint == "" {
		return monitoredRegistry{}, nil
	}

	target, err := parseRegistryTarget(endpoint, "")
	if err != nil {
		return monitoredRegistry{}, err
	}

	return monitoredRegistry{
		endpoint:             endpoint,
		target:               target,
		repository:           defaultManifestRepo,
		reference:            defaultManifestReference,
		manifestAcceptHeader: defaultManifestMediaType,
		headers:              map[string]string{},
	}, nil
}

func parseMonitoredRegistryMap(value map[string]any) (monitoredRegistry, error) {
	var cfg monitoredRegistryConfig

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "mapstructure",
		Result:           &cfg,
		WeaklyTypedInput: true,
	})
	if err != nil {
		return monitoredRegistry{}, fmt.Errorf(
			"failed to create registry proxy entry decoder: %w",
			err,
		)
	}

	if err := decoder.Decode(value); err != nil {
		return monitoredRegistry{}, fmt.Errorf("invalid registry proxy entry %v: %w", value, err)
	}

	if strings.TrimSpace(cfg.Endpoint) == "" {
		return monitoredRegistry{}, errors.New("invalid registry proxy entry: endpoint is required")
	}

	target, err := parseRegistryTarget(cfg.Endpoint, cfg.Scheme)
	if err != nil {
		return monitoredRegistry{}, err
	}

	repository := normalizeRepository(cfg.Repository)
	if repository == "" {
		repository = defaultManifestRepo
	}

	reference := strings.TrimSpace(cfg.Reference)
	if reference == "" {
		reference = defaultManifestReference
	}

	manifestAcceptHeader := strings.TrimSpace(cfg.ManifestAcceptHeader)
	if manifestAcceptHeader == "" {
		manifestAcceptHeader = defaultManifestMediaType
	}

	headers := map[string]string{}
	for name, value := range cfg.Headers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		headers[name] = value
	}

	return monitoredRegistry{
		endpoint:             strings.TrimSpace(cfg.Endpoint),
		info:                 strings.TrimSpace(cfg.Info),
		target:               target,
		repository:           repository,
		reference:            reference,
		manifestAcceptHeader: manifestAcceptHeader,
		headers:              headers,
	}, nil
}

func parseRegistryTarget(endpoint, schemeOverride string) (registryTarget, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return registryTarget{}, nil
	}

	scheme := normalizeRegistryScheme(schemeOverride)
	if err := validateRegistryScheme(scheme); err != nil {
		return registryTarget{}, err
	}

	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return registryTarget{}, fmt.Errorf(
				"invalid registry proxy endpoint %q: %w",
				endpoint,
				err,
			)
		}

		if parsed.Scheme == "" || parsed.Host == "" {
			return registryTarget{}, fmt.Errorf(
				"invalid registry proxy endpoint %q: scheme and host are required",
				endpoint,
			)
		}

		if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" ||
			parsed.Fragment != "" ||
			parsed.User != nil {
			return registryTarget{}, fmt.Errorf(
				"invalid registry proxy endpoint %q: only scheme, host, and port are supported",
				endpoint,
			)
		}

		scheme = normalizeRegistryScheme(parsed.Scheme)
		if err := validateRegistryScheme(scheme); err != nil {
			return registryTarget{}, err
		}

		raw = parsed.Host
	}

	host, port, err := splitRegistryHostPort(raw, endpoint, scheme)
	if err != nil {
		return registryTarget{}, err
	}

	return newRegistryTarget(scheme, host, port, endpoint)
}

func splitRegistryHostPort(raw, original, scheme string) (string, int, error) {
	host, port, err := net.SplitHostPort(raw)
	if err == nil {
		portNum, err := parsePort(port, original)
		if err != nil {
			return "", 0, err
		}

		return host, portNum, nil
	}

	var addrErr *net.AddrError
	if errors.As(err, &addrErr) {
		switch addrErr.Err {
		case "missing port in address":
			return strings.Trim(raw, "[]"), defaultPortForScheme(scheme), nil
		case "too many colons in address":
			if net.ParseIP(raw) != nil {
				return raw, defaultPortForScheme(scheme), nil
			}

			return "", 0, fmt.Errorf(
				"invalid registry proxy endpoint %q: IPv6 addresses with ports must use [host]:port",
				original,
			)
		}
	}

	return "", 0, fmt.Errorf("invalid registry proxy endpoint %q: %w", original, err)
}

func newRegistryTarget(scheme, host string, port int, original string) (registryTarget, error) {
	host = strings.TrimSpace(host)

	host = strings.Trim(host, "[]")
	if host == "" {
		return registryTarget{}, fmt.Errorf(
			"invalid registry proxy endpoint %q: host is required",
			original,
		)
	}

	if port < 1 || port > 65535 {
		return registryTarget{}, fmt.Errorf(
			"invalid registry proxy endpoint %q: port %d out of range",
			original,
			port,
		)
	}

	return registryTarget{
		Scheme: scheme,
		Host:   host,
		Port:   port,
	}, nil
}

func normalizeRegistryScheme(scheme string) string {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme == "" {
		return defaultRegistryScheme
	}

	return scheme
}

func validateRegistryScheme(scheme string) error {
	switch normalizeRegistryScheme(scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf(
			"unsupported registry proxy scheme %q: only http and https are supported",
			scheme,
		)
	}
}

func defaultPortForScheme(scheme string) int {
	switch normalizeRegistryScheme(scheme) {
	case "https":
		return 443
	default:
		return defaultRegistryPort
	}
}

func normalizeRepository(repository string) string {
	repository = strings.TrimSpace(repository)
	repository = strings.Trim(repository, "/")

	return repository
}

func parsePort(port, original string) (int, error) {
	if strings.TrimSpace(port) == "" {
		return 0, fmt.Errorf("invalid registry proxy endpoint %q: port is required", original)
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf(
			"invalid registry proxy endpoint %q: invalid port %q",
			original,
			port,
		)
	}

	if portNum < 1 || portNum > 65535 {
		return 0, fmt.Errorf(
			"invalid registry proxy endpoint %q: port %d out of range",
			original,
			portNum,
		)
	}

	return portNum, nil
}

func registryAuthority(host string, port int) string {
	if strings.Contains(host, ":") {
		return net.JoinHostPort(host, strconv.Itoa(port))
	}

	return net.JoinHostPort(host, strconv.Itoa(port))
}

func registryBaseURL(target registryTarget) string {
	return (&url.URL{
		Scheme: target.Scheme,
		Host:   registryAuthority(target.Host, target.Port),
	}).String()
}

func registryAPIURL(registry monitoredRegistry) string {
	base := registryBaseURL(registry.target)

	return base + "/v2/"
}

func registryManifestURL(registry monitoredRegistry) string {
	base := registryBaseURL(registry.target)
	repository := normalizeRepository(registry.repository)
	reference := url.PathEscape(registry.reference)

	return fmt.Sprintf("%s/v2/%s/manifests/%s", base, repository, reference)
}
