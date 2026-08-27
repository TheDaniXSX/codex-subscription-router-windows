package chromenative

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const ConfigFileName = "chrome-native-host.config.json"

var extensionIDPattern = regexp.MustCompile(`^[a-p]{32}$`)

type Config struct {
	Schema         int    `json:"schemaVersion"`
	HostName       string `json:"hostName"`
	ExtensionID    string `json:"extensionId"`
	LauncherConfig string `json:"launcherConfig"`
	BuildManifest  string `json:"buildManifest"`
	StateRoot      string `json:"-"`
	ControlPort    int    `json:"-"`
}

func LoadConfig(path string) (Config, error) {
	var config Config
	contents, err := os.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("read config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return config, errors.New("config contains more than one JSON value")
		}
		return config, fmt.Errorf("decode trailing config JSON: %w", err)
	}
	if err := config.validateIdentity(); err != nil {
		return config, err
	}
	if !filepath.IsAbs(config.LauncherConfig) || !filepath.IsAbs(config.BuildManifest) {
		return config, errors.New("launcherConfig and buildManifest must be absolute")
	}
	launcher, err := loadRouterContract(config.LauncherConfig)
	if err != nil {
		return config, fmt.Errorf("load launcher config: %w", err)
	}
	build, err := loadRouterContract(config.BuildManifest)
	if err != nil {
		return config, fmt.Errorf("load build manifest: %w", err)
	}
	if launcher.SchemaVersion != 2 || build.SchemaVersion != 2 {
		return config, errors.New("launcher and build manifests must use schemaVersion 2")
	}
	if launcher.ControlPort != build.ControlPort {
		return config, errors.New("launcher and build manifest controlPort values differ")
	}
	config.StateRoot = launcher.StateRoot
	config.ControlPort = launcher.ControlPort
	if err := config.Validate(); err != nil {
		return config, err
	}
	return config, nil
}

type routerContract struct {
	SchemaVersion int    `json:"schemaVersion"`
	StateRoot     string `json:"stateRoot"`
	ControlPort   int    `json:"controlPort"`
}

func loadRouterContract(path string) (routerContract, error) {
	var contract routerContract
	contents, err := os.ReadFile(path)
	if err != nil {
		return contract, err
	}
	if err := json.Unmarshal(contents, &contract); err != nil {
		return contract, err
	}
	return contract, nil
}

func (config Config) validateIdentity() error {
	if config.Schema != 2 {
		return fmt.Errorf("unsupported config schema %d", config.Schema)
	}
	if config.HostName != HostName {
		return fmt.Errorf("unexpected native host name %q", config.HostName)
	}
	if !extensionIDPattern.MatchString(config.ExtensionID) {
		return errors.New("extensionId must be 32 lowercase letters in the range a-p")
	}
	return nil
}

func (config Config) Validate() error {
	if err := config.validateIdentity(); err != nil {
		return err
	}
	if !filepath.IsAbs(config.StateRoot) {
		return errors.New("stateRoot must be absolute")
	}
	if config.ControlPort < 49152 || config.ControlPort > 65535 {
		return errors.New("controlPort must be in the dynamic/private range 49152..65535")
	}
	return nil
}

func (config Config) ControlURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", config.ControlPort)
}

func ExpectedOrigin(extensionID string) string {
	return "chrome-extension://" + extensionID + "/"
}

func ValidateOrigin(actual, extensionID string) error {
	if actual != ExpectedOrigin(extensionID) {
		return fmt.Errorf("caller origin %q is not authorized", actual)
	}
	return nil
}
