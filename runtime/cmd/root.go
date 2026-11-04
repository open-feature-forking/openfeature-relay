package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/dimiro1/banner"
	"github.com/mattn/go-colorable"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	Version string
	Commit  string
	Date    string
	Debug   bool
	// Distribution is the name of the distribution, which `flagd version --verbose` shows.
	Distribution string
	// ComponentsHint makes the startup errors that name a missing component point to the components command.
	ComponentsHint bool
)

var rootCmd = &cobra.Command{
	Use: "flagd",
	Short: "Flagd is a simple command line tool for fetching and presenting feature flags to services. " +
		"It is designed to conform to Open Feature schema for flag definitions.",
	Long:              ``,
	DisableAutoGenTag: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if viper.GetString(logFormatFlagName) == "console" {
			banner.InitString(colorable.NewColorableStdout(), true, true, `
	{{ .AnsiColor.BrightRed }}	 ______   __       ________   _______    ______      
	{{ .AnsiColor.BrightRed }}	/_____/\ /_/\     /_______/\ /______/\  /_____/\     
	{{ .AnsiColor.BrightRed }}	\::::_\/_\:\ \    \::: _  \ \\::::__\/__\:::_ \ \    
	{{ .AnsiColor.BrightRed }}	 \:\/___/\\:\ \    \::(_)  \ \\:\ /____/\\:\ \ \ \   
	{{ .AnsiColor.BrightRed }}	  \:::._\/ \:\ \____\:: __  \ \\:\\_  _\/ \:\ \ \ \  
	{{ .AnsiColor.BrightRed }}	   \:\ \    \:\/___/\\:.\ \  \ \\:\_\ \ \  \:\/.:| | 
	{{ .AnsiColor.BrightRed }}	    \_\/     \_____\/ \__\/\__\/ \_____\/   \____/_/ 
{{ .AnsiColor.Default }}
`)
		}
	},
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Components are the components of the distribution; Execute sets them.
var Components distribution.Components

// Inventory lists the components of the distribution; Execute sets it.
var Inventory distribution.Inventory

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main() with what the build of the distribution stamped, the components it contains and the
// inventory of them. It only needs to happen once to the rootCmd.
func Execute(info distribution.Info, components distribution.Components, inventory distribution.Inventory) {
	Distribution = info.Name
	Version = info.Version
	Commit = info.Commit
	Date = info.Date
	ComponentsHint = info.ComponentsHint
	Components = components
	Inventory = inventory
	err := rootCmd.Execute()
	if err != nil {
		log.Fatal(err)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
	rootCmd.PersistentFlags().BoolVarP(&Debug, "debug", "x", false, "verbose logging")
	// Bind the cobra flag to viper key
	if err := viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug")); err != nil {
		fmt.Fprintln(os.Stderr, "error when binding flags", err.Error())
	}
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.agent.yaml)")
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(componentsCmd)
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if invokesComponents(os.Args[1:]) {
		return
	}
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".agent" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".agent")
	}

	viper.AutomaticEnv() // read in environment variables that match
	Debug = viper.GetBool("debug")

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "using config file:", viper.ConfigFileUsed())
	}
}
