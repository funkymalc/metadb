package main

import (
	"fmt"
	"os"

	"github.com/metadb-project/metadb/cmd/internal/color"
	"github.com/metadb-project/metadb/cmd/internal/common"
	"github.com/metadb-project/metadb/cmd/internal/eout"
	"github.com/metadb-project/metadb/cmd/metadb/initsys"
	"github.com/metadb-project/metadb/cmd/metadb/log"
	"github.com/metadb-project/metadb/cmd/metadb/option"
	"github.com/metadb-project/metadb/cmd/metadb/server"
	"github.com/metadb-project/metadb/cmd/metadb/stop"
	"github.com/metadb-project/metadb/cmd/metadb/sysdb"
	"github.com/metadb-project/metadb/cmd/metadb/util"
	"github.com/spf13/cobra"
)

var program = "metadb"

// metadbVersion is defined at build time via -ldflags.
var metadbVersion string = "(unknown version)"

var colorMode string
var devMode bool

var colorInitialized bool

func main() {
	colorMode = os.Getenv("METADB_COLOR")
	devMode = (os.Getenv("METADB_DEV") == "on")
	metadbMain()
}

func metadbMain() {
	// Initialize error output
	eout.Init(program)
	// Run
	var err error
	if err = run(os.Args); err != nil {
		if !colorInitialized {
			color.NeverColor()
		}
		eout.Error("%s", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var globalOpt = option.Global{}
	var initOpt = option.Init{}
	var serverOpt = option.Server{}
	var stopOpt = option.Stop{}
	//var upgradeOpt = option.Upgrade{}
	var logfile, csvlogfile string

	var cmdInit = &cobra.Command{
		Use: "init",
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			if err = initColor(); err != nil {
				return err
			}
			initOpt.Global = globalOpt
			if err = initsys.InitSys(&initOpt); err != nil {
				return err
			}
			return nil
		},
	}
	cmdInit.SetHelpFunc(help)
	_ = dirFlag(cmdInit, &initOpt.Datadir)
	_ = verboseFlag(cmdInit, &eout.EnableVerbose)
	_ = traceFlag(cmdInit, &eout.EnableTrace)

	var cmdStart = &cobra.Command{
		Use: "start",
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			if err = initColor(); err != nil {
				return err
			}
			serverOpt.Global = globalOpt
			if err = validateServerOptions(&serverOpt); err != nil {
				return err
			}
			if err = sysdb.Init(util.SysdbFileName(serverOpt.Datadir)); err != nil {
				return err
			}
			var logf, csvlogf *os.File
			if logf, csvlogf, err = setupLog(logfile, csvlogfile, serverOpt.Debug, serverOpt.Trace); err != nil {
				return err
			}
			//if serverOpt.AdminPort == "" {
			//        serverOpt.AdminPort = metadbAdminPort
			//}
			serverOpt.MetadbVersion = metadbVersion
			if err = server.Start(&serverOpt); err != nil {
				return logFatal(err, logf, csvlogf)
			}
			log.Info("server is shut down")
			return nil
		},
	}
	cmdStart.SetHelpFunc(help)
	_ = dirFlag(cmdStart, &serverOpt.Datadir)
	_ = logFlag(cmdStart, &logfile)
	_ = csvlogFlag(cmdStart, &csvlogfile)
	_ = listenFlag(cmdStart, &serverOpt.Listen)
	_ = adminPortFlag(cmdStart, &serverOpt.AdminPort)
	_ = certFlag(cmdStart, &serverOpt.TLSCert)
	_ = keyFlag(cmdStart, &serverOpt.TLSKey)
	_ = debugFlag(cmdStart, &serverOpt.Debug)
	_ = traceLogFlag(cmdStart, &serverOpt.Trace)
	_ = noKafkaCommitFlag(cmdStart, &serverOpt.NoKafkaCommit)
	_ = sourceFileFlag(cmdStart, &serverOpt.SourceFilename)
	_ = logSourceFlag(cmdStart, &serverOpt.LogSource)
	_ = noTLSFlag(cmdStart, &serverOpt.NoTLS)

	var cmdStop = &cobra.Command{
		Use: "stop",
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			if err = initColor(); err != nil {
				return err
			}
			stopOpt.Global = globalOpt
			if err = stop.Stop(&stopOpt); err != nil {
				return err
			}
			return nil
		},
	}
	cmdStop.SetHelpFunc(help)
	_ = dirFlag(cmdStop, &stopOpt.Datadir)
	_ = verboseFlag(cmdStop, &eout.EnableVerbose)
	_ = traceFlag(cmdStop, &eout.EnableTrace)

	/*
		var cmdUpgrade = &cobra.Command{
			Use: "upgrade",
			//Short: "Upgrade database to the current version",
			RunE: func(cmd *cobra.Command, args []string) error {
				var err error
				if err = initColor(); err != nil {
					return err
				}
				upgradeOpt.Global = globalOpt
				//if err = runUpgradeDatabase(&upgradeOpt); err != nil {
				//        return err
				//}
				return nil
			},
		}
		cmdUpgrade.Flags().SortFlags = false
		cmdUpgrade.Flags().StringVarP(&upgradeOpt.Datadir, "dir", "D", "", ""+
			"Data directory name")
		cmdUpgrade.Flags().BoolVarP(&eout.EnableVerbose, "verbose", "v", false,
			"Enable verbose output")
		cmdUpgrade.Flags().BoolVar(&eout.EnableTrace, "xtrace", false,
			"Enable extremely verbose output")
		_ = cmdUpgrade.Flags().MarkHidden("xtrace")
	*/

	var cmdVersion = &cobra.Command{
		Use: "version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("metadb version %s\n", metadbVersion)
			return nil
		},
	}
	cmdVersion.SetHelpFunc(help)

	var cmdCompletion = &cobra.Command{
		Use:                   "completion",
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.ExactValidArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			switch args[0] {
			case "bash":
				cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
		},
	}
	cmdCompletion.SetHelpFunc(help)

	var rootCmd = &cobra.Command{
		Use:                "metadb",
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableSuggestions: true,
	}
	rootCmd.SetHelpFunc(help)
	// Redefine help flag without -h; so we can use it for
	// something else.
	var helpFlag bool
	rootCmd.PersistentFlags().BoolVarP(&helpFlag, "help", "", false, "Help for metadb")
	//rootCmd.PersistentFlags().StringVar(&_, "admin", metadbAdminPort, ""+
	//        "admin port")
	//rootCmd.PersistentFlags().StringVar(&_, "client", metadbClientPort, ""+
	//        "client port")
	// Add commands.
	rootCmd.AddCommand(cmdStart, cmdStop /* cmdUpgrade, */, cmdInit, cmdVersion, cmdCompletion)
	var err error
	if err = rootCmd.Execute(); err != nil {
		return err
	}

	return nil
}

var helpStart = "Start server\n"
var helpStop = "Shutdown server\n"
var helpInit = "Initialize new Metadb instance\n"
var helpVersion = "Print metadb version\n"
var helpCompletion = "Generate command-line completion\n"

func help(cmd *cobra.Command, commandLine []string) {
	switch cmd.Use {
	case "metadb":
		fmt.Printf("" +
			"Metadb server\n" +
			"\n" +
			"Usage:  metadb <command> [<flags>]\n" +
			"\n" +
			"Commands:\n" +
			"  start                       - " + helpStart +
			"  stop                        - " + helpStop +
			"  init                        - " + helpInit +
			"  version                     - " + helpVersion +
			"  completion                  - " + helpCompletion +
			"\n" +
			"Use \"metadb help <command>\" for more information about a command.\n")
	case "start":
		fmt.Printf("" +
			helpStart +
			"\n" +
			"Usage:  metadb start <flags>\n" +
			"\n" +
			"Options:\n" +
			dirFlag(nil, nil) +
			logFlag(nil, nil) +
			csvlogFlag(nil, nil) +
			listenFlag(nil, nil) +
			adminPortFlag(nil, nil) +
			certFlag(nil, nil) +
			keyFlag(nil, nil) +
			debugFlag(nil, nil) +
			noTLSFlag(nil, nil) +
			traceLogFlag(nil, nil) +
			noKafkaCommitFlag(nil, nil) +
			sourceFileFlag(nil, nil) +
			logSourceFlag(nil, nil) +
			"")
	case "stop":
		fmt.Printf("" +
			helpStop +
			"\n" +
			"Usage:  metadb stop <flags>\n" +
			"\n" +
			"Options:\n" +
			dirFlag(nil, nil) +
			verboseFlag(nil, nil) +
			traceFlag(nil, nil) +
			"")
	case "init":
		fmt.Printf("" +
			helpInit +
			"\n" +
			"Usage:  metadb init <flags>\n" +
			"\n" +
			"Options:\n" +
			dirFlag(nil, nil) +
			verboseFlag(nil, nil) +
			traceFlag(nil, nil) +
			"")
	case "version":
		fmt.Printf("" +
			helpVersion +
			"\n" +
			"Usage:  metadb version\n")
	case "completion":
		fmt.Printf("" +
			helpCompletion +
			"\n" +
			"Usage:  metadb completion <shell>\n" +
			"\n" +
			"Shells:\n" +
			"  bash\n" +
			"  zsh\n" +
			"  fish\n" +
			"  powershell\n")
	default:
	}
}

func verboseFlag(cmd *cobra.Command, verbose *bool) string {
	if cmd != nil {
		cmd.Flags().BoolVarP(verbose, "verbose", "v", false, "")
	}
	return "" +
		"-v, --verbose                 - Enable verbose output\n"
}

func debugFlag(cmd *cobra.Command, debug *bool) string {
	if cmd != nil {
		cmd.Flags().BoolVar(debug, "debug", false, "")
	}
	return "" +
		"    --debug                   - Enable detailed logging\n"
}

func traceFlag(cmd *cobra.Command, trace *bool) string {
	if devMode {
		if cmd != nil {
			cmd.Flags().BoolVar(trace, "trace", false, "")
		}
		return "" +
			"    --trace                   - Enable extremely verbose output\n"
	}
	return ""
}

func noKafkaCommitFlag(cmd *cobra.Command, noKafkaCommit *bool) string {
	if devMode {
		if cmd != nil {
			cmd.Flags().BoolVar(noKafkaCommit, "nokcommit", false, "")
		}
		return "" +
			"    --nokcommit               - Do not commit Kafka offsets\n"
	}
	return ""
}

func logSourceFlag(cmd *cobra.Command, logfile *string) string {
	if devMode {
		if cmd != nil {
			cmd.Flags().StringVar(logfile, "logsource", "", "")
		}
		return "" +
			"    --logsource <f>           - Log source messages to file\n"
	}
	return ""
}

func sourceFileFlag(cmd *cobra.Command, sourcefile *string) string {
	if devMode {
		if cmd != nil {
			cmd.Flags().StringVar(sourcefile, "sourcefile", "", "")
		}
		return "" +
			"    --sourcefile <f>          - Read source data from file\n"
	}
	return ""
}

func traceLogFlag(cmd *cobra.Command, trace *bool) string {
	if devMode {
		if cmd != nil {
			cmd.Flags().BoolVar(trace, "trace", false, "")
		}
		return "" +
			"    --trace                   - Enable extremely detailed logging\n"
	}
	return ""
}

func listenFlag(cmd *cobra.Command, listen *string) string {
	if cmd != nil {
		cmd.Flags().StringVar(listen, "listen", "", "")
	}
	return "" +
		"    --listen <a>              - Address to listen on (default: 127.0.0.1)\n"
}

func adminPortFlag(cmd *cobra.Command, adminPort *string) string {
	if cmd != nil {
		cmd.Flags().StringVar(adminPort, "admin-port", common.DefaultAdminPort, "")
	}
	return "" +
		"    --admin-port <p>          - Admin port to listen on (default: " + common.DefaultAdminPort + ")\n"
}

func certFlag(cmd *cobra.Command, cert *string) string {
	if cmd != nil {
		cmd.Flags().StringVar(cert, "cert", "", "")
	}
	return "" +
		"    --cert <f>                - File name of server certificate, including the\n" +
		"                                CA's certificate and intermediates\n"
}

func keyFlag(cmd *cobra.Command, key *string) string {
	if cmd != nil {
		cmd.Flags().StringVar(key, "key", "", "")
	}
	return "" +
		"    --key <f>                 - File name of server private key\n"
}

func logFlag(cmd *cobra.Command, logfile *string) string {
	if cmd != nil {
		cmd.Flags().StringVarP(logfile, "log", "l", "", "")
	}
	return "" +
		"-l, --log <f>                 - File name for server log output\n"
}

func csvlogFlag(cmd *cobra.Command, logfile *string) string {
	if cmd != nil {
		cmd.Flags().StringVar(logfile, "csvlog", "", "")
	}
	return "" +
		"    --csvlog <f>              - File name for server log CSV output\n"
}

func dirFlag(cmd *cobra.Command, datadir *string) string {
	if cmd != nil {
		cmd.Flags().StringVarP(datadir, "dir", "D", "", "")
	}
	return "" +
		"-D, --dir <d>                 - Data directory name\n"
}

func noTLSFlag(cmd *cobra.Command, noTLS *bool) string {
	if cmd != nil {
		cmd.Flags().BoolVar(noTLS, "no-tls", false, "")
	}
	return "" +
		"    --no-tls                  - Disable TLS in client connections [insecure,\n" +
		"                                use for testing only]\n"
}

func setupLog(logfile, csvlogfile string, debug bool, trace bool) (*os.File, *os.File, error) {
	var err error
	var logf, csvlogf *os.File
	if logfile != "" || csvlogfile != "" {
		log.DisableColor = true
		if logfile != "" {
			if logf, err = log.OpenLogFile(logfile); err != nil {
				return nil, nil, err
			}
		}
		if csvlogfile != "" {
			if csvlogf, err = log.OpenLogFile(csvlogfile); err != nil {
				return nil, nil, err
			}
		}
		log.Init(logf, csvlogf, debug, trace)
		return logf, csvlogf, nil
	}
	log.Init(os.Stderr, nil, debug, trace)
	return nil, nil, nil
}

func validateServerOptions(opt *option.Server) error {
	var err error
	// Require datadir specified
	if opt.Datadir == "" {
		return fmt.Errorf("data directory not specified")
	}
	// Require datadir exists
	var exists bool
	if exists, err = util.FileExists(opt.Datadir); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("data directory not found: %s", opt.Datadir)
	}
	// Require certificate and key specified if not loopback and
	// TLS not disabled
	if opt.Listen != "" && !opt.NoTLS {
		if opt.TLSCert == "" {
			return fmt.Errorf("server certificate not specified")
		}
		if opt.TLSKey == "" {
			return fmt.Errorf("server private key not specified")
		}
	}
	// Reject certificate or key with loopback default
	if opt.Listen == "" {
		if opt.TLSCert != "" {
			return fmt.Errorf("server certificate specified, but no listen address")
		}
		if opt.TLSKey != "" {
			return fmt.Errorf("server key specified, but no listen address")
		}
	}
	// Reject certificate or key with TLS disabled
	if opt.NoTLS {
		if opt.TLSCert != "" {
			return fmt.Errorf("server certificate specified while disabling TLS")
		}
		if opt.TLSKey != "" {
			return fmt.Errorf("server key specified while disabling TLS")
		}
	}
	// Reject disabling TLS when using loopback
	if opt.NoTLS && opt.Listen == "" {
		return fmt.Errorf("disabling TLS is not needed for loopback")
	}
	return nil
}

func logFatal(err error, logf, csvlogf *os.File) error {
	log.Fatal("%s", err)
	if logf != nil {
		logf.Close()
	}
	if csvlogf != nil {
		csvlogf.Close()
	}
	return fmt.Errorf("server stopped: %s", err)
}

func initColor() error {
	switch colorMode {
	case "always":
		color.AlwaysColor()
	case "auto":
		color.AutoColor()
	default:
		color.NeverColor()
	}
	colorInitialized = true
	return nil
}