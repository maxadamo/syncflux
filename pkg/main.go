package main

import (
	"flag"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/spf13/viper"

	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/toni-moreno/syncflux/pkg/agent"
	"github.com/toni-moreno/syncflux/pkg/config"
	"github.com/toni-moreno/syncflux/pkg/webui"
)

var (
	log  = logrus.New()
	quit = make(chan struct{})
	//startTime  = time.Now()
	getversion bool
	httpPort   = "0.0.0.0:4090"
	appdir     = os.Getenv("PWD")
	homeDir    string
	pidFile    string
	logDir     = filepath.Join(appdir, "log")
	confDir    = filepath.Join(appdir, "conf")
	dataDir    = confDir
	configFile = filepath.Join(confDir, "syncflux.toml")
	//
	action       = "hamonitor"
	actiondb     = "all"
	starttimestr string
	starttime    = time.Now().Add(-3600)
	endtimestr   string
	endtime      = time.Now()
)

func writePIDFile() {
	if pidFile == "" {
		return
	}

	// Ensure the required directory structure exists.
	err := os.MkdirAll(filepath.Dir(pidFile), 0700)
	if err != nil {
		log.Fatal(3, "Failed to verify pid directory", err)
	}

	// Retrieve the PID and write it.
	pid := strconv.Itoa(os.Getpid())
	if err := ioutil.WriteFile(pidFile, []byte(pid), 0644); err != nil {
		log.Fatal(3, "Failed to write pidfile", err)
	}
}

func flags() *flag.FlagSet {
	var f flag.FlagSet
	f.BoolVar(&getversion, "version", getversion, "display the version")
	//--------------------------------------------------------------
	f.StringVar(&action, "action", action, "hamonitor(default),copy,move,replicateschema")
	f.StringVar(&actiondb, "db", actiondb, "set the db where to play")
	f.StringVar(&starttimestr, "start", starttimestr, "set the starttime to do action (no valid in hamonitor) default now-24h")
	f.StringVar(&endtimestr, "end", endtimestr, "set the endtime do action (no valid in hamonitor) default now")
	//--------------------------------------------------------------
	f.StringVar(&configFile, "config", configFile, "config file")
	f.StringVar(&logDir, "logs", logDir, "log directory")
	f.StringVar(&homeDir, "home", homeDir, "home directory")
	f.StringVar(&dataDir, "data", dataDir, "Data directory")
	f.StringVar(&pidFile, "pidfile", pidFile, "path to pid file")
	//---------------------------------------------------------------
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		f.VisitAll(func(flag *flag.Flag) {
			format := "%10s: %s\n"
			fmt.Fprintf(os.Stderr, format, "-"+flag.Name, flag.Usage)
		})
		fmt.Fprintf(os.Stderr, "\nAll settings can be set in config file: %s\n", configFile)
		os.Exit(1)

	}
	return &f
}

func init() {
	//Log format
	customFormatter := new(logrus.TextFormatter)
	customFormatter.TimestampFormat = "2006-01-02 15:04:05"
	log.Formatter = customFormatter
	customFormatter.FullTimestamp = true

	// parse first time to see if config file is being specified
	f := flags()
	f.Parse(os.Args[1:])

	if getversion {
		t, _ := strconv.ParseInt(agent.BuildStamp, 10, 64)
		fmt.Printf("syncflux v%s (git: %s ) built at [%s]\n", agent.Version, agent.Commit, time.Unix(t, 0).Format("2006-01-02 15:04:05"))
		os.Exit(0)
	}

	// now load up config settings
	if _, err := os.Stat(configFile); err == nil {
		viper.SetConfigFile(configFile)
		confDir = filepath.Dir(configFile)
	} else {
		viper.SetConfigName("syncflux")
		viper.AddConfigPath("/etc/syncflux/")
		viper.AddConfigPath("/opt/syncflux/conf/")
		viper.AddConfigPath("./conf/")
		viper.AddConfigPath(".")
	}
	err := viper.ReadInConfig()
	if err != nil {
		log.Errorf("Fatal error config file: %s \n", err)
		os.Exit(1)
	}
	err = viper.Unmarshal(&agent.MainConfig)
	if err != nil {
		log.Errorf("Fatal error config file: %s \n", err)
		os.Exit(1)
	}
	cfg := &agent.MainConfig

	log.Infof("CFG :%+v", cfg)

	if len(logDir) == 0 {
		logDir = cfg.General.LogDir

	}

	if action == "hamonitor" {
		os.Mkdir(logDir, 0755)
		//Log output
		file, _ := os.OpenFile(logDir+"/syncflux.log", os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
		log.Out = file
		log.Infof("Set logdir %s from Config File", logDir)
	}

	if len(cfg.General.LogLevel) > 0 {
		l, _ := logrus.ParseLevel(cfg.General.LogLevel)
		log.Level = l
		log.Infof("Set log level to  %s from Config File", cfg.General.LogLevel)
	}
	if len(dataDir) == 0 {
		dataDir = cfg.General.DataDir
		log.Infof("Set DataDir %s from Config File", dataDir)
	}
	if len(homeDir) == 0 {
		homeDir = cfg.General.HomeDir
		log.Infof("Set HomeDir %s from Config File", homeDir)
	}
	//check if exist public dir in home
	if _, err := os.Stat(filepath.Join(homeDir, "public")); err != nil {
		log.Warnf("There is no public (www) directory on [%s] directory", homeDir)
		if len(homeDir) == 0 {
			homeDir = appdir
		}
	}
	//needed to create SQLDB when SQLite and debug log
	config.SetLogger(log)
	config.SetDirs(dataDir, logDir, confDir)

	webui.SetLogger(log)
	webui.SetLogDir(logDir)
	webui.SetConfDir(confDir)
	agent.SetLogger(log)

	//
	log.Infof("Set Default directories : \n   - Exec: %s\n   - Config: %s\n   -Logs: %s\n -Home: %s\n", appdir, confDir, logDir, homeDir)
}

func main() {

	defer func() {
		//errorLog.Close()
	}()
	writePIDFile()
	//Init BD config
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case sig := <-c:
			switch sig {
			case syscall.SIGTERM:
				log.Infof("Received TERM signal")
				agent.End()
				log.Infof("Exiting for requested user SIGTERM")
				os.Exit(1)
			case syscall.SIGHUP:
				log.Infof("Received HUP signal")
				agent.ReloadConf()
			}

		}
	}()

	var err error

	//parse input data

	if len(endtimestr) > 0 {
		endtime, err = parseInputTime(endtimestr)
		if err != nil {
			fmt.Printf("ERROR in Parse End Time : %s", err)
			os.Exit(1)
		}
	}

	if len(starttimestr) > 0 {
		starttime, err = parseInputTime(starttimestr)
		if err != nil {
			fmt.Printf("ERROR in Parse End Time : %s", err)
			os.Exit(1)
		}
	}

	switch action {
	case "hamonitor":
		agent.HAMonitorStart()
		webui.WebServer(filepath.Join(homeDir, "public"), httpPort, &agent.MainConfig.HTTP, agent.MainConfig.General.InstanceID)
	case "copy":
		agent.Copy(actiondb, starttime, endtime)
	case "move":
	case "replicateschema":
	default:
		fmt.Printf("Unknown action: %s", action)
	}

}
