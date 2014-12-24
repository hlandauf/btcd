// Copyright (c) 2013-2014 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	flags "github.com/conformal/go-flags"
	socks "github.com/conformal/go-socks"
	"github.com/hlandauf/btcdb"
	_ "github.com/hlandauf/btcdb/ldb"
	_ "github.com/hlandauf/btcdb/memdb"
	"github.com/hlandauf/btcnet"
	"github.com/hlandauf/btcnode"
	"github.com/hlandauf/btcserver"
	"github.com/hlandauf/btcmgmt"
	"github.com/hlandauf/btcutil"
	"github.com/hlandauf/btcwire"
)

const (
	defaultConfigFilename    = "btcd.conf"
	defaultDataDirname       = "data"
	defaultLogLevel          = "info"
	defaultLogDirname        = "logs"
	defaultLogFilename       = "btcd.log"
	defaultMaxPeers          = 125
	defaultBanDuration       = time.Hour * 24
	defaultMaxRPCClients     = 10
	defaultMaxRPCWebsockets  = 25
	defaultVerifyEnabled     = false
	defaultDbType            = "leveldb"
	defaultFreeTxRelayLimit  = 15.0
	defaultBlockMinSize      = 0
	defaultBlockMaxSize      = 750000
	blockMaxSizeMin          = 1000
	blockMaxSizeMax          = btcwire.MaxBlockPayload - 1000
	defaultBlockPrioritySize = 50000
	defaultGenerate          = false
)

var (
	btcdHomeDir        = btcutil.AppDataDir("btcd-nmc", false)
	defaultConfigFile  = filepath.Join(btcdHomeDir, defaultConfigFilename)
	defaultDataDir     = filepath.Join(btcdHomeDir, defaultDataDirname)
	knownDbTypes       = btcdb.SupportedDBs()
	defaultRPCKeyFile  = filepath.Join(btcdHomeDir, "rpc.key")
	defaultRPCCertFile = filepath.Join(btcdHomeDir, "rpc.cert")
	defaultLogDir      = filepath.Join(btcdHomeDir, defaultLogDirname)
)

// runServiceCommand is only set to a real function on Windows.  It is used
// to parse and execute service commands specified via the -s flag.
var runServiceCommand func(string) error

// serviceOptions defines the configuration options for btcd as a service on
// Windows.
type serviceOptions struct {
	ServiceCommand string `short:"s" long:"service" description:"Service command {install, remove, start, stop}"`
}

// cleanAndExpandPath expands environment variables and leading ~ in the
// passed path, cleans the result, and returns it.
func cleanAndExpandPath(path string) string {
	// Expand initial ~ to OS specific home directory.
	if strings.HasPrefix(path, "~") {
		homeDir := filepath.Dir(btcdHomeDir)
		path = strings.Replace(path, "~", homeDir, 1)
	}

	// NOTE: The os.ExpandEnv doesn't work with Windows-style %VARIABLE%,
	// but they variables can still be expanded via POSIX-style $VARIABLE.
	return filepath.Clean(os.ExpandEnv(path))
}

// validDbType returns whether or not dbType is a supported database type.
func validDbType(dbType string) bool {
	for _, knownType := range knownDbTypes {
		if dbType == knownType {
			return true
		}
	}

	return false
}

// removeDuplicateAddresses returns a new slice with all duplicate entries in
// addrs removed.
func removeDuplicateAddresses(addrs []string) []string {
	result := make([]string, 0, len(addrs))
	seen := map[string]struct{}{}
	for _, val := range addrs {
		if _, ok := seen[val]; !ok {
			result = append(result, val)
			seen[val] = struct{}{}
		}
	}
	return result
}

// normalizeAddress returns addr with the passed default port appended if
// there is not already a port specified.
func normalizeAddress(addr, defaultPort string) string {
	_, _, err := net.SplitHostPort(addr)
	if err != nil {
		return net.JoinHostPort(addr, defaultPort)
	}
	return addr
}

// normalizeAddresses returns a new slice with all the passed peer addresses
// normalized with the given default port, and all duplicates removed.
func normalizeAddresses(addrs []string, defaultPort string) []string {
	for i, addr := range addrs {
		addrs[i] = normalizeAddress(addr, defaultPort)
	}

	return removeDuplicateAddresses(addrs)
}

// filesExists reports whether the named file or directory exists.
func fileExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// newConfigParser returns a new command line flags parser.
func newConfigParser(cfg *btcserver.Config, so *serviceOptions, options flags.Options) *flags.Parser {
	parser := flags.NewParser(cfg, options)
	if runtime.GOOS == "windows" {
		parser.AddGroup("Service Options", "Service Options", so)
	}

	//parser.AddGroup("Node Options", &cfg.NodeConfig)

	return parser
}

func netName(netParams *btcnet.Params) string {
	switch netParams.Net {
	case btcwire.TestNet3:
		return "testnet"
	default:
		return netParams.Name
	}
}

// minUint32 is a helper function to return the minimum of two uint32s.
// This avoids a math import and the need to cast to floats.
func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

// loadConfig initializes and parses the config using a config file and command
// line options.
//
// The configuration proceeds as follows:
// 	1) Start with a default config with sane settings
// 	2) Pre-parse the command line to check for an alternative config file
// 	3) Load configuration file overwriting defaults with any specified options
// 	4) Parse CLI options and overwrite/add any specified options
//
// The above results in btcd functioning properly without any config settings
// while still allowing the user to override settings with config files and
// command line options.  Command line options always take precedence.
func loadConfig() (*btcserver.Config, []string, error) {
	// Default config.
	cfg := btcserver.Config{
		NodeConfig: btcnode.NodeConfig{
			ConfigFile:        defaultConfigFile,
			DebugLevel:        defaultLogLevel,
			MaxPeers:          defaultMaxPeers,
			BanDuration:       defaultBanDuration,
			DataDir:           defaultDataDir,
			LogDir:            defaultLogDir,
			DbType:            defaultDbType,
			FreeTxRelayLimit:  defaultFreeTxRelayLimit,
			BlockMinSize:      defaultBlockMinSize,
			BlockMaxSize:      defaultBlockMaxSize,
			BlockPrioritySize: defaultBlockPrioritySize,
			Generate:          defaultGenerate,
		},
		RPCConfig: btcmgmt.RPCServerConfig{
			MaxClients:    defaultMaxRPCClients,
			MaxWebsockets: defaultMaxRPCWebsockets,
			Key:           defaultRPCKeyFile,
			Cert:          defaultRPCCertFile,
		},
	}

	//cfg.initLogging()

	// Service options which are only added on Windows.
	serviceOpts := serviceOptions{}

	// Create the home directory if it doesn't already exist.
	err := os.MkdirAll(btcdHomeDir, 0700)
	if err != nil {
		log.Errorf("%v", err)
		return nil, nil, err
	}

	// Pre-parse the command line options to see if an alternative config
	// file or the version flag was specified.  Any errors aside from the
	// help message error can be ignored here since they will be caught by
	// the final parse below.
	preCfg := cfg
	preParser := newConfigParser(&preCfg, &serviceOpts, flags.HelpFlag|flags.IgnoreUnknown)
	_, err = preParser.Parse()
	if err != nil {
		if e, ok := err.(*flags.Error); ok && e.Type == flags.ErrHelp {
			fmt.Fprintln(os.Stderr, err)
			return nil, nil, err
		}
	}

	// Show the version and exit if the version flag was specified.
	appName := filepath.Base(os.Args[0])
	appName = strings.TrimSuffix(appName, filepath.Ext(appName))
	usageMessage := fmt.Sprintf("Use %s -h to show usage", appName)
	if preCfg.ShowVersion {
		fmt.Println(appName, "version", version())
		os.Exit(0)
	}

	// Perform service command and exit if specified.  Invalid service
	// commands show an appropriate error.  Only runs on Windows since
	// the runServiceCommand function will be nil when not on Windows.
	if serviceOpts.ServiceCommand != "" && runServiceCommand != nil {
		err := runServiceCommand(serviceOpts.ServiceCommand)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(0)
	}

	// Load additional config from file.
	var configFileError error
	parser := newConfigParser(&cfg, &serviceOpts, flags.Default|flags.IgnoreUnknown)
	if !(preCfg.RegressionTest || preCfg.SimNet) || preCfg.ConfigFile !=
		defaultConfigFile {

		err := flags.NewIniParser(parser).ParseFile(preCfg.ConfigFile)
		if err != nil {
			if _, ok := err.(*os.PathError); !ok {
				fmt.Fprintf(os.Stderr, "Error parsing config "+
					"file: %v\n", err)
				fmt.Fprintln(os.Stderr, usageMessage)
				return nil, nil, err
			}
			configFileError = err
		}
	}

	// Don't add peers from the config file when in regression test mode.
	if preCfg.RegressionTest && len(cfg.AddPeers) > 0 {
		cfg.AddPeers = nil
	}

	// Parse command line options again to ensure they take precedence.
	remainingArgs, err := parser.Parse()
	if err != nil {
		if e, ok := err.(*flags.Error); !ok || e.Type != flags.ErrHelp {
			fmt.Fprintln(os.Stderr, usageMessage)
		}
		return nil, nil, err
	}

	// Multiple networks can't be selected simultaneously.
	funcName := "loadConfig"
	numNets := 0
	// Count number of network flags passed; assign active network params
	// while we're at it
	cfg.ActiveNetParams = &btcnet.NmcMainNetParams

	if cfg.TestNet3 {
		numNets++
		cfg.ActiveNetParams = &btcnet.TestNet3Params
	}
	if cfg.RegressionTest {
		numNets++
		cfg.ActiveNetParams = &btcnet.RegressionNetParams
	}
	if cfg.SimNet {
		numNets++
		// Also disable dns seeding on the simulation test network.
		cfg.ActiveNetParams = &btcnet.SimNetParams
		cfg.DisableDNSSeed = true
	}
	if numNets > 1 {
		str := "%s: The testnet, regtest, and simnet params can't be " +
			"used together -- choose one of the three"
		err := fmt.Errorf(str, funcName)
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// Append the network type to the data directory so it is "namespaced"
	// per network.  In addition to the block database, there are other
	// pieces of data that are saved to disk such as address manager state.
	// All data is specific to a network, so namespacing the data directory
	// means each individual piece of serialized data does not have to
	// worry about changing names per network and such.
	cfg.DataDir = cleanAndExpandPath(cfg.DataDir)
	cfg.DataDir = filepath.Join(cfg.DataDir, netName(cfg.ActiveNetParams))

	// Append the network type to the log directory so it is "namespaced"
	// per network in the same fashion as the data directory.
	cfg.LogDir = cleanAndExpandPath(cfg.LogDir)
	cfg.LogDir = filepath.Join(cfg.LogDir, netName(cfg.ActiveNetParams))

	// Special show command to list supported subsystems and exit.
	/*if cfg.DebugLevel == "show" {
		fmt.Println("Supported subsystems", cfg.supportedSubsystems())
		os.Exit(0)
	}*/

	// Initialize logging at the default logging level.
	//cfg.initSeelogLogger(filepath.Join(cfg.LogDir, defaultLogFilename))
	//cfg.setLogLevels(defaultLogLevel)

	// Parse, validate, and set debug log level(s).
	/*if err := cfg.parseAndSetDebugLevels(cfg.DebugLevel); err != nil {
		err := fmt.Errorf("%s: %v", funcName, err.Error())
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}*/

	// Validate database type.
	if !validDbType(cfg.DbType) {
		str := "%s: The specified database type [%v] is invalid -- " +
			"supported types %v"
		err := fmt.Errorf(str, funcName, cfg.DbType, knownDbTypes)
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// Validate profile port number
	if cfg.Profile != "" {
		profilePort, err := strconv.Atoi(cfg.Profile)
		if err != nil || profilePort < 1024 || profilePort > 65535 {
			str := "%s: The profile port must be between 1024 and 65535"
			err := fmt.Errorf(str, funcName)
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintln(os.Stderr, usageMessage)
			return nil, nil, err
		}
	}

	// Don't allow ban durations that are too short.
	if cfg.BanDuration < time.Duration(time.Second) {
		str := "%s: The banduration option may not be less than 1s -- parsed [%v]"
		err := fmt.Errorf(str, funcName, cfg.BanDuration)
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// --addPeer and --connect do not mix.
	if len(cfg.AddPeers) > 0 && len(cfg.ConnectPeers) > 0 {
		str := "%s: the --addpeer and --connect options can not be " +
			"mixed"
		err := fmt.Errorf(str, funcName)
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// --proxy or --connect without --listen disables listening.
	if (cfg.Proxy != "" || len(cfg.ConnectPeers) > 0) &&
		len(cfg.Listeners) == 0 {
		cfg.DisableListen = true
	}

	// Connect means no DNS seeding.
	if len(cfg.ConnectPeers) > 0 {
		cfg.DisableDNSSeed = true
	}

	// Add the default listener if none were specified. The default
	// listener is all addresses on the listen port for the network
	// we are to connect to.
	if len(cfg.Listeners) == 0 {
		cfg.Listeners = []string{
			net.JoinHostPort("", cfg.ActiveNetParams.DefaultPort),
		}
	}

	// The RPC server is disabled if no username or password is provided.
	if cfg.RPCConfig.User == "" || cfg.RPCConfig.Pass == "" {
		cfg.DisableRPC = true
	}

	// Default RPC to listen on localhost only.
	if !cfg.DisableRPC && len(cfg.RPCConfig.Listeners) == 0 {
		addrs, err := net.LookupHost("localhost")
		if err != nil {
			return nil, nil, err
		}
		cfg.RPCConfig.Listeners = make([]string, 0, len(addrs))
		for _, addr := range addrs {
			addr = net.JoinHostPort(addr, cfg.ActiveNetParams.RPCPort)
			cfg.RPCConfig.Listeners = append(cfg.RPCConfig.Listeners, addr)
		}

	}

	// Limit the max block size to a sane value.
	if cfg.BlockMaxSize < blockMaxSizeMin || cfg.BlockMaxSize >
		blockMaxSizeMax {

		str := "%s: The blockmaxsize option must be in between %d " +
			"and %d -- parsed [%d]"
		err := fmt.Errorf(str, funcName, blockMaxSizeMin,
			blockMaxSizeMax, cfg.BlockMaxSize)
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// Limit the block priority and minimum block sizes to max block size.
	cfg.BlockPrioritySize = minUint32(cfg.BlockPrioritySize, cfg.BlockMaxSize)
	cfg.BlockMinSize = minUint32(cfg.BlockMinSize, cfg.BlockMaxSize)

	// Check getwork keys are valid and saved parsed versions.
	cfg.MiningAddrsS = make([]btcutil.Address, 0, len(cfg.GetWorkKeys)+
		len(cfg.MiningAddrsS))
	for _, strAddr := range cfg.GetWorkKeys {
		addr, err := btcutil.DecodeAddress(strAddr,
			cfg.ActiveNetParams)
		if err != nil {
			str := "%s: getworkkey '%s' failed to decode: %v"
			err := fmt.Errorf(str, funcName, strAddr, err)
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintln(os.Stderr, usageMessage)
			return nil, nil, err
		}
		if !addr.IsForNet(cfg.ActiveNetParams) {
			str := "%s: getworkkey '%s' is on the wrong network"
			err := fmt.Errorf(str, funcName, strAddr)
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintln(os.Stderr, usageMessage)
			return nil, nil, err
		}
		cfg.MiningAddrsS = append(cfg.MiningAddrsS, addr)
	}

	// Check mining addresses are valid and saved parsed versions.
	for _, strAddr := range cfg.MiningAddrs {
		addr, err := btcutil.DecodeAddress(strAddr, cfg.ActiveNetParams)
		if err != nil {
			str := "%s: mining address '%s' failed to decode: %v"
			err := fmt.Errorf(str, funcName, strAddr, err)
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintln(os.Stderr, usageMessage)
			return nil, nil, err
		}
		if !addr.IsForNet(cfg.ActiveNetParams) {
			str := "%s: mining address '%s' is on the wrong network"
			err := fmt.Errorf(str, funcName, strAddr)
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintln(os.Stderr, usageMessage)
			return nil, nil, err
		}
		cfg.MiningAddrsS = append(cfg.MiningAddrsS, addr)
	}

	// Ensure there is at least one mining address when the generate flag is
	// set.
	if cfg.Generate && len(cfg.MiningAddrsS) == 0 {
		str := "%s: the generate flag is set, but there are no mining " +
			"addresses specified "
		err := fmt.Errorf(str, funcName)
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, usageMessage)
		return nil, nil, err
	}

	// Add default port to all listener addresses if needed and remove
	// duplicate addresses.
	cfg.Listeners = normalizeAddresses(cfg.Listeners,
		cfg.ActiveNetParams.DefaultPort)

	// Add default port to all rpc listener addresses if needed and remove
	// duplicate addresses.
	cfg.RPCConfig.Listeners = normalizeAddresses(cfg.RPCConfig.Listeners,
		cfg.ActiveNetParams.RPCPort)

	// Add default port to all added peer addresses if needed and remove
	// duplicate addresses.
	cfg.AddPeers = normalizeAddresses(cfg.AddPeers,
		cfg.ActiveNetParams.DefaultPort)
	cfg.ConnectPeers = normalizeAddresses(cfg.ConnectPeers,
		cfg.ActiveNetParams.DefaultPort)

	// Setup dial and DNS resolution (lookup) functions depending on the
	// specified options.  The default is to use the standard net.Dial
	// function as well as the system DNS resolver.  When a proxy is
	// specified, the dial function is set to the proxy specific dial
	// function and the lookup is set to use tor (unless --noonion is
	// specified in which case the system DNS resolver is used).
	cfg.Dial = net.Dial
	cfg.Lookup = net.LookupIP
	if cfg.Proxy != "" {
		proxy := &socks.Proxy{
			Addr:     cfg.Proxy,
			Username: cfg.ProxyUser,
			Password: cfg.ProxyPass,
		}
		cfg.Dial = proxy.Dial
		if !cfg.NoOnion {
			cfg.Lookup = func(host string) ([]net.IP, error) {
				return torLookupIP(host, cfg.Proxy)
			}
		}
	}

	// Setup onion address dial and DNS resolution (lookup) functions
	// depending on the specified options.  The default is to use the
	// same dial and lookup functions selected above.  However, when an
	// onion-specific proxy is specified, the onion address dial and
	// lookup functions are set to use the onion-specific proxy while
	// leaving the normal dial and lookup functions as selected above.
	// This allows .onion address traffic to be routed through a different
	// proxy than normal traffic.
	if cfg.OnionProxy != "" {
		cfg.Oniondial = func(a, b string) (net.Conn, error) {
			proxy := &socks.Proxy{
				Addr:     cfg.OnionProxy,
				Username: cfg.OnionProxyUser,
				Password: cfg.OnionProxyPass,
			}
			return proxy.Dial(a, b)
		}
		cfg.Onionlookup = func(host string) ([]net.IP, error) {
			return torLookupIP(host, cfg.OnionProxy)
		}
	} else {
		cfg.Oniondial = cfg.Dial
		cfg.Onionlookup = cfg.Lookup
	}

	// Specifying --noonion means the onion address dial and DNS resolution
	// (lookup) functions result in an error.
	if cfg.NoOnion {
		cfg.Oniondial = func(a, b string) (net.Conn, error) {
			return nil, errors.New("tor has been disabled")
		}
		cfg.Onionlookup = func(a string) ([]net.IP, error) {
			return nil, errors.New("tor has been disabled")
		}
	}

	// Warn about missing config file only after all other configuration is
	// done.  This prevents the warning on help messages and invalid
	// options.  Note this should go directly before the return.
	if configFileError != nil {
		log.Warnf("%v", configFileError)
	}

	return &cfg, remainingArgs, nil
}
