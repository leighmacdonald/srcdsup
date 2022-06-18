package config

import (
	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
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
	Host           string            `mapstructure:"host"`
	Path           string            `mapstructure:"path"`
	Port           int               `mapstructure:"port"`
	Type           RemoteServiceType `mapstructure:"type"`
	Root           string            `mapstructure:"root"`
	PrivateKeyPath string            `mapstructure:"private_key_path"`
}

type RulesConfig struct {
	Name   string `mapstructure:"name"`
	Src    string `mapstructure:"src"`
	Remote string `mapstructure:"remote"`
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
	Global = rootCfg
	if found {
		log.Infof("Using config file: %s", viper.ConfigFileUsed())
	} else {
		log.Warnf("No configuration found, defaults used")
	}
	return nil
}
