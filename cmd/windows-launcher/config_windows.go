//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	productDirectoryName   = "Codex Subscription Router"
	stateDirectoryName     = "Codex Subscription Router Data"
	profileDirectoryName   = "Profile"
	sidecarRelativePath    = `resources\codex-router\launcher-config.json`
	realAppName            = "ChatGPT.real.exe"
	muxRelativePath        = `resources\codex.exe`
	realCodexRelative      = `resources\codex.real.exe`
	selfTestArgument       = "--router-self-test"
	diagnosticsArgument    = "--router-diagnostics"
	maximumSidecarBytes    = 16 * 1024
	maximumDeepLinkBytes   = 4 * 1024
	appshotsEnvironment    = "CODEX_ROUTER_ENABLE_APPSHOTS"
	controlPortEnvironment = "CODEX_MUX_CONTROL_PORT"
	routerProtocolScheme   = "codex-router"
	routerOpenHost         = "open"
	legacyControlPort      = 48123
	minimumControlPort     = 49152
	maximumControlPort     = 65535
)

var stateRootEnvironmentPrecedence = []string{
	"CODEX_ROUTER_DATA_DIR",
	"CODEX_MUX_HOME",
	"CODEX_MUX_STATE_ROOT",
}

var strippedChildEnvironment = []string{
	"CODEX_MUX_CONTROL_TOKEN",
	"CODEX_MUX_UI_TESTS",
}

type sidecarConfiguration struct {
	SchemaVersion int    `json:"schemaVersion"`
	StateRoot     string `json:"stateRoot"`
	ControlPort   *int   `json:"controlPort,omitempty"`
}

type launchPlan struct {
	AppDirectory        string
	RealApp             string
	Mux                 string
	RealCodex           string
	StateRoot           string
	RootSource          string
	Profile             string
	ControlPort         int
	ConfigSchemaVersion int
	Arguments           []string
}

type optionalFileReader func(path string) (contents []byte, exists bool, err error)
type fileChecker func(path string) error
type environmentLookup func(key string) (value string, exists bool)

func buildLaunchPlan(
	executable string,
	arguments []string,
	lookup environmentLookup,
	readOptional optionalFileReader,
	checkFile fileChecker,
) (launchPlan, error) {
	if executable == "" {
		return launchPlan{}, errors.New("launcher executable path is empty")
	}
	normalizedArguments, err := normalizeRouterArguments(arguments, checkOpenTarget)
	if err != nil {
		return launchPlan{}, fmt.Errorf("validate router deep link: %w", err)
	}
	if hasUserDataOverride(normalizedArguments) {
		return launchPlan{}, errors.New("external --user-data-dir overrides are not allowed")
	}
	absoluteExecutable, err := filepath.Abs(executable)
	if err != nil {
		return launchPlan{}, fmt.Errorf("resolve launcher executable: %w", err)
	}
	appDirectory := filepath.Dir(absoluteExecutable)
	stateRoot, rootSource, controlPort, configSchemaVersion, err := resolveLauncherConfiguration(
		appDirectory, lookup, readOptional,
	)
	if err != nil {
		return launchPlan{}, err
	}
	plan := launchPlan{
		AppDirectory:        appDirectory,
		RealApp:             filepath.Join(appDirectory, realAppName),
		Mux:                 filepath.Join(appDirectory, muxRelativePath),
		RealCodex:           filepath.Join(appDirectory, realCodexRelative),
		StateRoot:           stateRoot,
		RootSource:          rootSource,
		Profile:             filepath.Join(stateRoot, profileDirectoryName),
		ControlPort:         controlPort,
		ConfigSchemaVersion: configSchemaVersion,
		Arguments:           make([]string, 0, len(normalizedArguments)+1),
	}
	plan.Arguments = append(plan.Arguments, "--user-data-dir="+plan.Profile)
	plan.Arguments = append(plan.Arguments, normalizedArguments...)

	for label, path := range map[string]string{
		"ChatGPT.real.exe":         plan.RealApp,
		`resources\codex.exe`:      plan.Mux,
		`resources\codex.real.exe`: plan.RealCodex,
	} {
		if err := checkFile(path); err != nil {
			return launchPlan{}, fmt.Errorf("validate %s: %w", label, err)
		}
	}
	if sameWindowsPath(plan.Mux, plan.RealCodex) {
		return launchPlan{}, errors.New("resources\\codex.exe and resources\\codex.real.exe must be distinct")
	}
	return plan, nil
}

// resolveLauncherConfiguration reads the sidecar once so the state root and
// control port cannot be selected from different versions of a replaced file.
// New builds write schema 2. Schema 1 remains readable solely to permit a
// controlled migration of already-installed routers using the historical port.
func resolveLauncherConfiguration(
	appDirectory string,
	_ environmentLookup,
	readOptional optionalFileReader,
) (string, string, int, int, error) {
	sidecarPath := filepath.Join(appDirectory, sidecarRelativePath)
	contents, exists, err := readOptional(sidecarPath)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("read launcher sidecar %s: %w", sidecarPath, err)
	}
	if !exists {
		return "", "", 0, 0, fmt.Errorf(
			"launcher sidecar %s is required to select a control port", sidecarPath,
		)
	}
	configuration, err := decodeSidecar(contents)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("validate launcher sidecar %s: %w", sidecarPath, err)
	}
	controlPort, err := sidecarControlPort(configuration)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("validate launcher sidecar controlPort: %w", err)
	}

	root, err := validateAbsoluteRoot(configuration.StateRoot)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("validate launcher sidecar stateRoot: %w", err)
	}
	return root, "sidecar:" + sidecarRelativePath, controlPort, configuration.SchemaVersion, nil
}

func validateReleaseLaunchPlan(plan launchPlan) error {
	if plan.ConfigSchemaVersion != 2 {
		return fmt.Errorf(
			"launcher configuration schema %d is diagnostic-only; reinstall or upgrade to schema 2",
			plan.ConfigSchemaVersion,
		)
	}
	if plan.ControlPort < minimumControlPort || plan.ControlPort > maximumControlPort {
		return fmt.Errorf(
			"control port %d is outside the required dynamic range %d..%d",
			plan.ControlPort, minimumControlPort, maximumControlPort,
		)
	}
	return nil
}

// runReleaseLaunch is the single barrier before any mutable launch work or
// process creation. Keeping the callback behind validation makes the no-spawn
// guarantee independently testable without starting a synthetic or real app.
func runReleaseLaunch(plan launchPlan, launch func() error) error {
	if err := validateReleaseLaunchPlan(plan); err != nil {
		return err
	}
	if launch == nil {
		return errors.New("release launch callback is nil")
	}
	return launch()
}

// normalizeRouterArguments turns the router's private protocol into the same
// single path argument used by the optional Explorer verbs. Other arguments,
// including the official codex:// protocol, are preserved byte-for-byte.
func normalizeRouterArguments(arguments []string, checkTarget fileChecker) ([]string, error) {
	linkIndex := -1
	for index, argument := range arguments {
		separator := strings.IndexByte(argument, ':')
		if separator < 0 || !strings.EqualFold(argument[:separator], routerProtocolScheme) {
			continue
		}
		if linkIndex >= 0 {
			return nil, errors.New("more than one codex-router URI was supplied")
		}
		linkIndex = index
	}
	if linkIndex < 0 {
		return append([]string(nil), arguments...), nil
	}
	if len(arguments) != 1 {
		return nil, errors.New("a codex-router URI must be the only argument")
	}
	target, err := parseRouterOpenURI(arguments[linkIndex])
	if err != nil {
		return nil, err
	}
	if err := checkTarget(target); err != nil {
		return nil, fmt.Errorf("validate open target %q: %w", target, err)
	}
	return []string{target}, nil
}

func parseRouterOpenURI(raw string) (string, error) {
	if raw == "" || len(raw) > maximumDeepLinkBytes {
		return "", fmt.Errorf("URI length must be between 1 and %d bytes", maximumDeepLinkBytes)
	}
	if !utf8.ValidString(raw) || strings.IndexByte(raw, 0) >= 0 || strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("URI contains invalid text")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse URI: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, routerProtocolScheme) || parsed.Opaque != "" {
		return "", errors.New("URI must use the hierarchical codex-router scheme")
	}
	if parsed.User != nil || parsed.Port() != "" || !strings.EqualFold(parsed.Hostname(), routerOpenHost) {
		return "", errors.New("URI authority must be exactly codex-router://open")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("URI path is not supported")
	}
	if parsed.Fragment != "" {
		return "", errors.New("URI fragments are not supported")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", fmt.Errorf("parse URI query: %w", err)
	}
	if len(query) != 1 || len(query["path"]) != 1 || query.Get("path") == "" {
		return "", errors.New("URI must contain exactly one non-empty path parameter")
	}
	target := query.Get("path")
	if len(target) >= 32768 || !utf8.ValidString(target) || strings.IndexByte(target, 0) >= 0 || strings.ContainsAny(target, "\r\n\"") {
		return "", errors.New("open target contains invalid text")
	}
	if err := validateLocalPathShape(target); err != nil {
		return "", err
	}
	return filepath.Clean(target), nil
}

func validateLocalPathShape(target string) error {
	if strings.HasPrefix(target, `\\`) || strings.HasPrefix(target, `//`) || strings.HasPrefix(target, `\\?\`) || strings.HasPrefix(target, `\\.\`) {
		return errors.New("network and device paths are not supported")
	}
	volume := filepath.VolumeName(target)
	if len(volume) != 2 || volume[1] != ':' || !isASCIILetter(volume[0]) || !filepath.IsAbs(target) {
		return errors.New("open target must be an absolute local drive path")
	}
	remainder := strings.TrimPrefix(target, volume)
	if strings.Contains(remainder, ":") {
		return errors.New("alternate data stream paths are not supported")
	}
	for _, component := range strings.Split(strings.ReplaceAll(remainder, "/", `\`), `\`) {
		if component == "." || component == ".." {
			return errors.New("open target must not contain traversal components")
		}
	}
	return nil
}

func isASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func checkOpenTarget(path string) error {
	information, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !information.Mode().IsRegular() && !information.IsDir() {
		return errors.New("target is not a regular file or directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve target links: %w", err)
	}
	if err := validateLocalPathShape(resolved); err != nil {
		return fmt.Errorf("resolved target is not local: %w", err)
	}
	return nil
}

func resolveStateRoot(
	appDirectory string,
	lookup environmentLookup,
	readOptional optionalFileReader,
) (string, string, error) {
	for _, name := range stateRootEnvironmentPrecedence {
		if value, exists := lookup(name); exists && value != "" {
			root, err := validateAbsoluteRoot(value)
			if err != nil {
				return "", "", fmt.Errorf("validate %s: %w", name, err)
			}
			return root, "environment:" + name, nil
		}
	}

	sidecarPath := filepath.Join(appDirectory, sidecarRelativePath)
	contents, exists, err := readOptional(sidecarPath)
	if err != nil {
		return "", "", fmt.Errorf("read launcher sidecar %s: %w", sidecarPath, err)
	}
	if exists {
		configuration, err := decodeSidecar(contents)
		if err != nil {
			return "", "", fmt.Errorf("validate launcher sidecar %s: %w", sidecarPath, err)
		}
		root, err := validateAbsoluteRoot(configuration.StateRoot)
		if err != nil {
			return "", "", fmt.Errorf("validate launcher sidecar stateRoot: %w", err)
		}
		return root, "sidecar:" + sidecarRelativePath, nil
	}

	localAppData, exists := lookup("LOCALAPPDATA")
	if !exists || localAppData == "" {
		return "", "", errors.New("LOCALAPPDATA is not set and no explicit state root was provided")
	}
	defaultRoot, err := validateAbsoluteRoot(filepath.Join(localAppData, "Programs", stateDirectoryName))
	if err != nil {
		return "", "", fmt.Errorf("validate default state root: %w", err)
	}
	return defaultRoot, "default:LOCALAPPDATA", nil
}

func validateAbsoluteRoot(value string) (string, error) {
	if value == "" {
		return "", errors.New("path is empty")
	}
	if strings.IndexByte(value, 0) >= 0 {
		return "", errors.New("path contains a NUL byte")
	}
	cleaned := filepath.Clean(value)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path must be absolute: %q", value)
	}
	return cleaned, nil
}

func decodeSidecar(contents []byte) (sidecarConfiguration, error) {
	if len(contents) == 0 {
		return sidecarConfiguration{}, errors.New("sidecar is empty")
	}
	if len(contents) > maximumSidecarBytes {
		return sidecarConfiguration{}, fmt.Errorf("sidecar exceeds %d bytes", maximumSidecarBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var configuration sidecarConfiguration
	if err := decoder.Decode(&configuration); err != nil {
		return sidecarConfiguration{}, fmt.Errorf("decode JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return sidecarConfiguration{}, errors.New("sidecar contains more than one JSON value")
		}
		return sidecarConfiguration{}, fmt.Errorf("decode trailing JSON: %w", err)
	}
	if configuration.StateRoot == "" {
		return sidecarConfiguration{}, errors.New("stateRoot is required")
	}
	switch configuration.SchemaVersion {
	case 1:
		if configuration.ControlPort != nil {
			return sidecarConfiguration{}, errors.New("schemaVersion 1 must not contain controlPort")
		}
	case 2:
		if configuration.ControlPort == nil {
			return sidecarConfiguration{}, errors.New("schemaVersion 2 requires controlPort")
		}
		if _, err := sidecarControlPort(configuration); err != nil {
			return sidecarConfiguration{}, err
		}
	default:
		return sidecarConfiguration{}, fmt.Errorf("unsupported schemaVersion %d", configuration.SchemaVersion)
	}
	return configuration, nil
}

func sidecarControlPort(configuration sidecarConfiguration) (int, error) {
	if configuration.SchemaVersion == 1 && configuration.ControlPort == nil {
		return legacyControlPort, nil
	}
	if configuration.SchemaVersion != 2 || configuration.ControlPort == nil {
		return 0, errors.New("controlPort is unavailable for this sidecar schema")
	}
	port := *configuration.ControlPort
	if port < minimumControlPort || port > maximumControlPort {
		return 0, fmt.Errorf(
			"controlPort %d is outside the required dynamic range %d..%d",
			port, minimumControlPort, maximumControlPort,
		)
	}
	return port, nil
}

func hasUserDataOverride(arguments []string) bool {
	const option = "--user-data-dir"
	for _, argument := range arguments {
		lower := strings.ToLower(argument)
		if lower == option || strings.HasPrefix(lower, option+"=") {
			return true
		}
	}
	return false
}

func sameWindowsPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func readOptionalFile(path string) ([]byte, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximumSidecarBytes+1))
	if err != nil {
		return nil, true, err
	}
	if len(contents) > maximumSidecarBytes {
		return nil, true, fmt.Errorf("file exceeds %d bytes", maximumSidecarBytes)
	}
	return contents, true, nil
}

func checkRegularFile(path string) error {
	information, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !information.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	return nil
}

func checkDistinctFiles(left, right string) error {
	leftInformation, err := os.Stat(left)
	if err != nil {
		return fmt.Errorf("stat %s: %w", left, err)
	}
	rightInformation, err := os.Stat(right)
	if err != nil {
		return fmt.Errorf("stat %s: %w", right, err)
	}
	if os.SameFile(leftInformation, rightInformation) {
		return fmt.Errorf("%s and %s resolve to the same file", left, right)
	}
	return nil
}

func environmentWith(environment []string, replacements map[string]string) []string {
	result := make([]string, 0, len(environment)+len(replacements))
	normalized := make(map[string]string, len(replacements))
	for key, value := range replacements {
		normalized[strings.ToUpper(key)] = value
	}
	emitted := make(map[string]bool, len(normalized))
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			result = append(result, entry)
			continue
		}
		key := strings.ToUpper(entry[:separator])
		if value, replace := normalized[key]; replace {
			if !emitted[key] {
				result = append(result, entry[:separator+1]+value)
				emitted[key] = true
			}
			continue
		}
		result = append(result, entry)
	}
	remaining := make([]string, 0, len(normalized))
	for key := range normalized {
		if !emitted[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	for _, key := range remaining {
		result = append(result, key+"="+normalized[key])
	}
	return result
}

func childEnvironment(environment []string, plan launchPlan) []string {
	appshots := "0"
	if explicitEnvironmentOptIn(environment, appshotsEnvironment) {
		appshots = "1"
	}
	sanitized := environmentWithout(environment, strippedChildEnvironment)
	return environmentWith(sanitized, map[string]string{
		"CODEX_ROUTER_DATA_DIR":         plan.StateRoot,
		"CODEX_MUX_HOME":                plan.StateRoot,
		"CODEX_MUX_STATE_ROOT":          plan.StateRoot,
		"CODEX_ELECTRON_USER_DATA_PATH": plan.Profile,
		"CODEX_CLI_PATH":                plan.Mux,
		"CODEX_MUX_REAL_CODEX":          plan.RealCodex,
		"CODEX_SPARKLE_ENABLED":         "false",
		controlPortEnvironment:          strconv.Itoa(plan.ControlPort),
		appshotsEnvironment:             appshots,
	})
}

// environmentWithout strips security-sensitive developer overrides before the
// desktop and mux are created. The control token must come only from the
// persisted token file injected into the renderer at build time; UI test routes
// must never be activatable through a release launcher environment.
func environmentWithout(environment []string, names []string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[strings.ToUpper(name)] = struct{}{}
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator > 0 {
			if _, remove := blocked[strings.ToUpper(entry[:separator])]; remove {
				continue
			}
		}
		result = append(result, entry)
	}
	return result
}

// explicitEnvironmentOptIn requires every occurrence to be exactly "1". Windows
// environment keys are case-insensitive, so conflicting duplicates fail closed.
func explicitEnvironmentOptIn(environment []string, name string) bool {
	found := false
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 || !strings.EqualFold(entry[:separator], name) {
			continue
		}
		found = true
		if entry[separator+1:] != "1" {
			return false
		}
	}
	return found
}
