package config

import (
	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"strings"
	"time"
)

type RemoteServerConfig struct {
	PrivateKeyPath string `mapstructure:"private_key_path"`
	Username       string `mapstructure:"username"`
	Password       string `mapstructure:"password"`
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
}

type RulesConfig struct {
	Name       string `mapstructure:"name"`
	Src        string `mapstructure:"src"`
	Handler    string `mapstructure:"handler"`
	RemoteDest string `mapstructure:"dest"`
	LocalRoot  string `mapstructure:"local_root"`
	RemoteRoot string `mapstructure:"remote_root"`
	Suffix     string `mapstructure:"suffix"`
}

type RootConfig struct {
	UpdateInterval       time.Duration
	UpdateIntervalString string             `mapstructure:"update_interval"`
	RemoteDest           RemoteServerConfig `mapstructure:"remote_dest"`
	Rules                []RulesConfig      `mapstructure:"rules"`
}

var (
	Global RootConfig
)

// Read reads in config file and ENV variables if set.
func Read(cfgFiles ...string) error {
	// Find home directory.
	home, errHomeDir := homedir.Dir()
	if errHomeDir != nil {
		return errors.Wrapf(errHomeDir, "Failed to get HOME dir")
	}
	viper.AddConfigPath(home)
	viper.AddConfigPath(".")
	viper.SetConfigName("stvup")
	viper.SetEnvPrefix("stvup")
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
