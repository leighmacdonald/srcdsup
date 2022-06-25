package config

import (
	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

type RemoteServiceType string

const (
	SSH RemoteServiceType = "ssh"
	// HTTP         RemoteServiceType = "http"
	GBansDemos   RemoteServiceType = "gbans_demo"
	GBansGameLog RemoteServiceType = "gbans_log"
)

type RemoteConfig struct {
	Name           string            `mapstructure:"name"`
	Username       string            `mapstructure:"username"`
	Password       string            `mapstructure:"password"`
	Url            string            `mapstructure:"url"`
	Type           RemoteServiceType `mapstructure:"type"`
	Root           string            `mapstructure:"root"`
	PrivateKeyPath string            `mapstructure:"private_key_path"`
}

type RulesConfig struct {
	Name    string   `mapstructure:"name"`
	Root    string   `mapstructure:"root"`
	Pattern string   `mapstructure:"pattern"`
	Remotes []string `mapstructure:"remotes"`
	Server  string   `mapstructure:"server"`
}

func (rc RulesConfig) SrcFile(f fs.FileInfo) string {
	return filepath.Join(rc.Root, f.Name())
}

type RootConfig struct {
	UpdateInterval       time.Duration
	UpdateIntervalString string         `mapstructure:"update_interval"`
	Remotes              []RemoteConfig `mapstructure:"remotes"`
	Rules                []RulesConfig  `mapstructure:"rules"`
}

var Global RootConfig

// Read reads in config file and ENV variables if set.
func Read(cfgFiles ...string) error {
	// Find home directory.
	home, errHomeDir := homedir.Dir()
	if errHomeDir != nil {
		return errors.Wrapf(errHomeDir, "Failed to get HOME dir")
	}
	viper.AddConfigPath(home)
	viper.AddConfigPath(".")
	viper.SetConfigName("srcdsup")
	viper.SetEnvPrefix("srcdsup")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	found := false
	for _, cfgFilePath := range cfgFiles {
		viper.SetConfigFile(cfgFilePath)
		if errReadConfig := viper.ReadInConfig(); errReadConfig != nil {
			return errors.Wrapf(errReadConfig, "Failed to read config file: %s", cfgFilePath)
		}
		found = true
	}
	var rootCfg RootConfig
	if errUnmarshal := viper.Unmarshal(&rootCfg); errUnmarshal != nil {
		return errors.Wrap(errUnmarshal, "Invalid config syntax")
	}
	duration, errDuration := time.ParseDuration(rootCfg.UpdateIntervalString)
	if errDuration != nil {
		duration = time.Second * 60
		log.Warnf("Failed to parse update interval, using default of 60s: %v", errDuration)
	}
	rootCfg.UpdateInterval = duration

	for i, r := range rootCfg.Rules {
		absPath, errPath := filepath.Abs(r.Root)
		if errPath != nil {
			return errors.Wrapf(errPath, "Failed to get abs path for rule")
		}
		rootCfg.Rules[i].Root = absPath
	}

	Global = rootCfg
	if found {
		log.Infof("Using config file: %s", viper.ConfigFileUsed())
	} else {
		log.Warnf("No configuration found, defaults used")
	}
	return nil
}
