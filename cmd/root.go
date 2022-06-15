package cmd

import (
	"fmt"
	"github.com/leighmacdonald/srcdsup/config"
	"github.com/leighmacdonald/srcdsup/srcdsup"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"os"
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
		fmt.Println(errExecute)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(func() {
		if errConfig := config.Read(cfgFile); errConfig != nil {
			log.Fatalf("Failed to read config: %v", errConfig)
		}
	})
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
}
