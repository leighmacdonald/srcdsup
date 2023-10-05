package config

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type RemoteServiceType string

const (
	GBansDemos RemoteServiceType = "gbans_demo"
	// GBansGameLog RemoteServiceType = "gbans_log".
)

type RemoteConfig struct {
	Name           string            `mapstructure:"name"`
	Username       string            `mapstructure:"username"`
	Password       string            `mapstructure:"password"`
	AuthToken      string            `mapstructure:"-"`
	URL            string            `mapstructure:"url"`
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

func (rc *RulesConfig) SrcFile(f fs.FileInfo) string {
	return filepath.Join(rc.Root, f.Name())
}

type RootConfig struct {
	UpdateInterval       time.Duration
	UpdateIntervalString string          `mapstructure:"update_interval"`
	Remotes              []*RemoteConfig `mapstructure:"remotes"`
	Rules                []*RulesConfig  `mapstructure:"rules"`
}

var Global *RootConfig

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

	for _, cfgFilePath := range cfgFiles {
		viper.SetConfigFile(cfgFilePath)

		if errReadConfig := viper.ReadInConfig(); errReadConfig != nil {
			return errors.Wrapf(errReadConfig, "Failed to read config file: %s", cfgFilePath)
		}
	}

	var rootCfg RootConfig
	if errUnmarshal := viper.Unmarshal(&rootCfg); errUnmarshal != nil {
		return errors.Wrap(errUnmarshal, "Invalid config syntax")
	}

	duration, errDuration := time.ParseDuration(rootCfg.UpdateIntervalString)
	if errDuration != nil {
		duration = time.Second * 60
	}

	rootCfg.UpdateInterval = duration

	for index, rule := range rootCfg.Rules {
		absPath, errPath := filepath.Abs(rule.Root)
		if errPath != nil {
			return errors.Wrapf(errPath, "Failed to get abs path for rule")
		}

		rootCfg.Rules[index].Root = absPath
	}

	Global = &rootCfg

	return nil
}

func MustCreateLogger() *zap.Logger {
	loggingConfig := zap.NewProductionConfig()
	loggingConfig.DisableCaller = true
	loggingConfig.Level.SetLevel(zap.InfoLevel)

	l, errLogger := loggingConfig.Build()
	if errLogger != nil {
		panic("Failed to create log config")
	}

	return l.Named("srcdsup")
}
