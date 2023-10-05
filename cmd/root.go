package cmd

import (
	"fmt"

	"github.com/leighmacdonald/srcdsup/config"
	"github.com/leighmacdonald/srcdsup/srcdsup"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	rootCmd = &cobra.Command{
		Use:   "srcdsup",
		Short: "srcdsup",
		Long:  `SRCDS File Uploader`,
		Run: func(cmd *cobra.Command, args []string) {
			srcdsup.Start()
		},
	}
)

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if errExecute := rootCmd.Execute(); errExecute != nil {
		panic(errExecute)
	}
}

func init() {
	cobra.OnInitialize(func() {
		if errConfig := config.Read(cfgFile); errConfig != nil {
			panic(fmt.Sprintf("Failed to read config: %v", errConfig))
		}
	})

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
}
