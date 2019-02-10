package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"SiaPrime/build"
	"SiaPrime/modules"
	"SiaPrime/modules/consensus"
	"SiaPrime/modules/explorer"
	"SiaPrime/modules/gateway"
	"SiaPrime/modules/host"
	index "SiaPrime/modules/index"
	"SiaPrime/modules/miner"
	pool "SiaPrime/modules/miningpool"
	"SiaPrime/modules/renter"
	"SiaPrime/modules/stratumminer"
	"SiaPrime/modules/transactionpool"
	"SiaPrime/modules/wallet"
	"SiaPrime/node/api"
	"SiaPrime/types"

	"github.com/inconshreveable/go-update"
	"github.com/julienschmidt/httprouter"
	"github.com/kardianos/osext"
)

var errEmptyUpdateResponse = errors.New("API call to https://api.github.com/repos/SiaPrime/SiaPrime/releases/latest is returning an empty response")

type (
	// Server creates and serves a HTTP server that offers communication with a
	// Sia API.
	Server struct {
		httpServer    *http.Server
		listener      net.Listener
		config        Config
		moduleClosers []moduleCloser
		api           http.Handler
		mu            sync.Mutex
	}

	// moduleCloser defines a struct that closes modules, defined by a name and
	// an underlying io.Closer.
	moduleCloser struct {
		name string
		io.Closer
	}

	// SiaConstants is a struct listing all of the constants in use.
	SiaConstants struct {
		BlockFrequency         types.BlockHeight `json:"blockfrequency"`
		BlockSizeLimit         uint64            `json:"blocksizelimit"`
		ExtremeFutureThreshold types.Timestamp   `json:"extremefuturethreshold"`
		FutureThreshold        types.Timestamp   `json:"futurethreshold"`
		GenesisTimestamp       types.Timestamp   `json:"genesistimestamp"`
		MaturityDelay          types.BlockHeight `json:"maturitydelay"`
		MedianTimestampWindow  uint64            `json:"mediantimestampwindow"`
		SiafundCount           types.Currency    `json:"siafundcount"`
		SiafundPortion         *big.Rat          `json:"siafundportion"`
		TargetWindow           types.BlockHeight `json:"targetwindow"`

		InitialCoinbase uint64 `json:"initialcoinbase"`
		MinimumCoinbase uint64 `json:"minimumcoinbase"`

		RootTarget types.Target `json:"roottarget"`
		RootDepth  types.Target `json:"rootdepth"`

		// DEPRECATED: same values as MaxTargetAdjustmentUp and
		// MaxTargetAdjustmentDown.
		MaxAdjustmentUp   *big.Rat `json:"maxadjustmentup"`
		MaxAdjustmentDown *big.Rat `json:"maxadjustmentdown"`

		MaxTargetAdjustmentUp   *big.Rat `json:"maxtargetadjustmentup"`
		MaxTargetAdjustmentDown *big.Rat `json:"maxtargetadjustmentdown"`

		SiacoinPrecision types.Currency `json:"siacoinprecision"`
	}

	// DaemonVersion holds the version information for siad
	DaemonVersion struct {
		Version     string `json:"version"`
		GitRevision string `json:"gitrevision"`
		BuildTime   string `json:"buildtime"`
	}
	// UpdateInfo indicates whether an update is available, and to what
	// version.
	UpdateInfo struct {
		Available bool   `json:"available"`
		Version   string `json:"version"`
	}
	// gitlabRelease represents some of the JSON returned by the GitLab
	// release API endpoint. Only the fields relevant to updating are
	// included.
	gitlabRelease struct {
		TagName string `json:"name"`
	}
)

const (
	// The developer key is used to sign updates and other important SiaPrime-
	// related information.
	developerKey = `-----BEGIN PUBLIC KEY-----
MIIEIjANBgkqhkiG9w0BAQEFAAOCBA8AMIIECgKCBAEA05U9xFO9EgKEUQ5LzLkD
//iODWzBY+bKWEMial0hxHnFU/FD0DOqpjvbZV8NgF0rFZu6MznN+UO8+a8EqGiq
L3XkUmnJu9K2sWOJOv9aYYbFQwxay1gI+jIFsldjTVf7yOz+9IcM48zyX0A952Fx
y5ugctqr7Sr+2uuLAqXXyWwA8FQ5ZPWOzCySJCbcaKSP4fP+caRKCUeAci9CON6g
UxIGgO3H5KjBa1nt38XE4Qt7hy7peIRigmfm3FJkSnqWijkYUXrPIwILfqaFMknZ
40Yc5GEqxOdzSYoptWXfVdnoDvu58qJI5ikAwr/pIjVCSn3sdMudlDiRoiUfHCYn
T7OEq6wWSL/lmpOKSXavIxUwfs4mpCA/a9E0P1FICLr52DNrzVVveEXmkzUEtqlF
26FssMzTt6mkmAF8T7HRBtVyuTt+UD2I7oEJ29CPV0U7YE5KVhmQG7ae3i6k2csJ
mEdDNbb0rOdZrsygyfJNkG/IkD09PTn9KteTDc9ULnEhq2LXtot0y1/zas1z8BsC
rJbAdGU0o5/SpWPfOrw2O/aa0Ja413cMyGAXUZRiseTncLod9dKn9Y6GVIb3LSj7
D3FfV2ADG2vdpuBc18JsPDC+oLWjIjpEZKlyr3tqyQsE3lJjWteU2W+Tju4PWM9Q
GT/e9SY9DcFKTY8AIxrtzyuCGwuW5a3yQ9FP2yEDpF7j2POIU7LIEtnyn9oXP/6d
3y4X+DJhkDm4cKfV+OsVyCYwK3+IQvr1g1F2N/xsKByZbb4Jgwge5Libtrnc0GH8
WxdIlbNBHHuU58yI0GjPnFLqRMe0duPzJXWtq18RJFjUHg8muhb14lBuXOqaF0cs
gn+8izPgbSMhEHPQBhT9Ux+byU3qDjKqim3f5xtZNH6R2eT58N8y5CwXYYodpMVL
nhcX29QsqqpO1mZQwx6snSJ6QDvmcSrhEgFhWLAbUY6R+I0UWGY23dlI/cNEWOyu
zRSS/DDK0gV7+pPFT2+HiP6YkZ4qlHqmWIWXBFeXSAEzVNe2AG48BD3sXQGlh7Jd
x73jHo/17DZUivnQ8MyvOfDY1ThfwSNlCJzfYvQUxpa43JAZzCGxEDw7pJvrHqLD
cV75Zk12DzRBoC4neDRWd4qfXApx066Ew56xxeCQI5Z5suZ1juaejpr9BDshHyE4
Abt4U3mhMBtcdD4Oz0mo+iuXLePbdo3uwCGFE7GOb+XdxvegYzTE0w/24mXNluz8
BiFzhF0Dgq2SmUkVpqYehZNmfm61/xtyOM5uHOtphetSdA5lPaplSiLzi6piaaKa
huUdkWLWC9t9M+VuEZhk0ra/x7b1HDy4bLqBaXzJtKvZyRlOBWBFmIVmctygG6t9
ZwIDAQAB
-----END PUBLIC KEY-----`
)

// fetchLatestRelease returns metadata about the most recent GitLab release.
func fetchLatestRelease() (gitlabRelease, error) {
	resp, err := http.Get("https://gitlab.com/api/v4/projects/10135403/repository/tags?order_by=name")
	if err != nil {
		return gitlabRelease{}, err
	}
	defer resp.Body.Close()
	var releases []gitlabRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return gitlabRelease{}, err
	} else if len(releases) == 0 {
		return gitlabRelease{}, errors.New("no releases found")
	}
	return releases[0], nil
}

// updateToRelease updates siad and siac to the release specified. siac is
// assumed to be in the same folder as siad.
func updateToRelease(version string) error {
	updateOpts := update.Options{
		Verifier: update.NewRSAVerifier(),
	}
	err := updateOpts.SetPublicKeyPEM([]byte(developerKey))
	if err != nil {
		// should never happen
		return err
	}

	binaryFolder, err := osext.ExecutableFolder()
	if err != nil {
		return err
	}

	// download release archive
	resp, err := http.Get(fmt.Sprintf("https://siaprime.net/releases/Sia-%s-%s-%s.zip", version, runtime.GOOS, runtime.GOARCH))
	if err != nil {
		return err
	}
	// release should be small enough to store in memory (<10 MiB); use
	// LimitReader to ensure we don't download more than 32 MiB
	content, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1<<25))
	resp.Body.Close()
	if err != nil {
		return err
	}
	r := bytes.NewReader(content)
	z, err := zip.NewReader(r, r.Size())
	if err != nil {
		return err
	}

	// process zip, finding siad/siac binaries and signatures
	for _, binary := range []string{"spd", "spc"} {
		var binData io.ReadCloser
		var signature []byte
		var binaryName string // needed for TargetPath below
		for _, zf := range z.File {
			switch base := path.Base(zf.Name); base {
			case binary, binary + ".exe":
				binaryName = base
				binData, err = zf.Open()
				if err != nil {
					return err
				}
				defer binData.Close()
			case binary + ".sig", binary + ".exe.sig":
				sigFile, err := zf.Open()
				if err != nil {
					return err
				}
				defer sigFile.Close()
				signature, err = ioutil.ReadAll(sigFile)
				if err != nil {
					return err
				}
			}
		}
		if binData == nil {
			return errors.New("could not find " + binary + " binary")
		} else if signature == nil {
			return errors.New("could not find " + binary + " signature")
		}

		// apply update
		updateOpts.Signature = signature
		updateOpts.TargetMode = 0775 // executable
		updateOpts.TargetPath = filepath.Join(binaryFolder, binaryName)
		err = update.Apply(binData, updateOpts)
		if err != nil {
			return err
		}
	}

	return nil
}

// daemonUpdateHandlerGET handles the API call that checks for an update.
func (srv *Server) daemonUpdateHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	release, err := fetchLatestRelease()
	if err != nil {
		api.WriteError(w, api.Error{Message: "Failed to fetch latest release: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	latestVersion := release.TagName[1:] // delete leading 'v'
	api.WriteJSON(w, UpdateInfo{
		Available: build.VersionCmp(latestVersion, build.Version) > 0,
		Version:   latestVersion,
	})
}

// daemonUpdateHandlerPOST handles the API call that updates siad and siac.
// There is no safeguard to prevent "updating" to the same release, so callers
// should always check the latest version via daemonUpdateHandlerGET first.
// TODO: add support for specifying version to update to.
func (srv *Server) daemonUpdateHandlerPOST(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	release, err := fetchLatestRelease()
	if err != nil {
		api.WriteError(w, api.Error{Message: "Failed to fetch latest release: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	err = updateToRelease(release.TagName)
	if err != nil {
		if rerr := update.RollbackError(err); rerr != nil {
			api.WriteError(w, api.Error{Message: "Serious error: Failed to rollback from bad update: " + rerr.Error()}, http.StatusInternalServerError)
		} else {
			api.WriteError(w, api.Error{Message: "Failed to apply update: " + err.Error()}, http.StatusInternalServerError)
		}
		return
	}
	api.WriteSuccess(w)
}

// debugConstantsHandler prints a json file containing all of the constants.
func (srv *Server) daemonConstantsHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	sc := SiaConstants{
		BlockFrequency:         types.BlockFrequency,
		BlockSizeLimit:         types.BlockSizeLimit,
		ExtremeFutureThreshold: types.ExtremeFutureThreshold,
		FutureThreshold:        types.FutureThreshold,
		GenesisTimestamp:       types.GenesisTimestamp,
		MaturityDelay:          types.MaturityDelay,
		MedianTimestampWindow:  types.MedianTimestampWindow,
		SiafundCount:           types.SiafundCount,
		SiafundPortion:         types.SiafundPortion,
		TargetWindow:           types.TargetWindow,

		InitialCoinbase: types.InitialCoinbase,
		MinimumCoinbase: types.MinimumCoinbase,

		RootTarget: types.RootTarget,
		RootDepth:  types.RootDepth,

		// DEPRECATED: same values as MaxTargetAdjustmentUp and
		// MaxTargetAdjustmentDown.
		MaxAdjustmentUp:   types.MaxTargetAdjustmentUp,
		MaxAdjustmentDown: types.MaxTargetAdjustmentDown,

		MaxTargetAdjustmentUp:   types.MaxTargetAdjustmentUp,
		MaxTargetAdjustmentDown: types.MaxTargetAdjustmentDown,

		SiacoinPrecision: types.SiacoinPrecision,
	}

	api.WriteJSON(w, sc)
}

// daemonVersionHandler handles the API call that requests the daemon's version.
func (srv *Server) daemonVersionHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	version := build.Version
	if build.ReleaseTag != "" {
		version += "-" + build.ReleaseTag
	}
	api.WriteJSON(w, DaemonVersion{Version: version, GitRevision: build.GitRevision, BuildTime: build.BuildTime})
}

// daemonStopHandler handles the API call to stop the daemon cleanly.
func (srv *Server) daemonStopHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// can't write after we stop the server, so lie a bit.
	api.WriteSuccess(w)

	// need to flush the response before shutting down the server
	f, ok := w.(http.Flusher)
	if !ok {
		panic("Server does not support flushing")
	}
	f.Flush()

	if err := srv.Close(); err != nil {
		build.Critical(err)
	}
}

func (srv *Server) daemonHandler(password string) http.Handler {
	router := httprouter.New()

	router.GET("/daemon/constants", srv.daemonConstantsHandler)
	router.GET("/daemon/version", srv.daemonVersionHandler)
	router.GET("/daemon/update", srv.daemonUpdateHandlerGET)
	router.POST("/daemon/update", srv.daemonUpdateHandlerPOST)
	router.GET("/daemon/stop", api.RequirePassword(srv.daemonStopHandler, password))

	return router
}

// apiHandler handles all calls to the API. If the ready flag is not set, this
// will return an error. Otherwise it will serve the api.
func (srv *Server) apiHandler(w http.ResponseWriter, r *http.Request) {
	srv.mu.Lock()
	isReady := srv.api != nil
	srv.mu.Unlock()
	if !isReady {
		api.WriteError(w, api.Error{Message: "spd is not ready. please wait for spd to finish loading."}, http.StatusServiceUnavailable)
		return
	}
	srv.api.ServeHTTP(w, r)
}

// NewServer creates a new net.http server listening on bindAddr.  Only the
// /daemon/ routes are registered by this func, additional routes can be
// registered later by calling serv.mux.Handle.
func NewServer(config Config) (*Server, error) {
	// Process the config variables after they are parsed by cobra.
	config, err := processConfig(config)
	if err != nil {
		return nil, err
	}
	// Create the listener for the server
	l, err := net.Listen("tcp", config.Siad.APIaddr)
	if err != nil {
		if isAddrInUseErr(err) {
			return nil, fmt.Errorf("%v; are you running another instance of spd?", err.Error())
		}

		return nil, err
	}

	// Create the Server
	mux := http.NewServeMux()
	srv := &Server{
		listener: l,
		httpServer: &http.Server{
			Handler: mux,

			// set reasonable timeout windows for requests, to prevent the Sia API
			// server from leaking file descriptors due to slow, disappearing, or
			// unreliable API clients.

			// ReadTimeout defines the maximum amount of time allowed to fully read
			// the request body. This timeout is applied to every handler in the
			// server.
			ReadTimeout: time.Minute * 5,

			// ReadHeaderTimeout defines the amount of time allowed to fully read the
			// request headers.
			ReadHeaderTimeout: time.Minute * 2,

			// IdleTimeout defines the maximum duration a HTTP Keep-Alive connection
			// the API is kept open with no activity before closing.
			IdleTimeout: time.Minute * 5,
		},
		config: config,
	}

	// Register siad routes
	mux.Handle("/daemon/", api.RequireUserAgent(srv.daemonHandler(config.APIPassword), config.Siad.RequiredUserAgent))
	mux.HandleFunc("/", srv.apiHandler)

	return srv, nil
}

// isAddrInUseErr checks if the error corresponds to syscall.EADDRINUSE
func isAddrInUseErr(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if syscallErr, ok := opErr.Err.(*os.SyscallError); ok {
			return syscallErr.Err == syscall.EADDRINUSE
		}
	}
	return false
}

// loadModules loads the modules defined by the server's config and makes their
// API routes available.
func (srv *Server) loadModules() error {
	// Create the server and start serving daemon routes immediately.
	fmt.Printf("(0/%d) Loading spd...\n", len(srv.config.Siad.Modules))

	// Initialize the Sia modules
	i := 0
	var err error
	var g modules.Gateway
	if strings.Contains(srv.config.Siad.Modules, "g") {
		i++
		fmt.Printf("(%d/%d) Loading gateway...\n", i, len(srv.config.Siad.Modules))
		g, err = gateway.New(srv.config.Siad.RPCaddr, !srv.config.Siad.NoBootstrap, filepath.Join(srv.config.Siad.SiaDir, modules.GatewayDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "gateway", Closer: g})
	}
	var cs modules.ConsensusSet
	if strings.Contains(srv.config.Siad.Modules, "c") {
		i++
		fmt.Printf("(%d/%d) Loading consensus...\n", i, len(srv.config.Siad.Modules))
		cs, err = consensus.New(g, !srv.config.Siad.NoBootstrap, filepath.Join(srv.config.Siad.SiaDir, modules.ConsensusDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "consensus", Closer: cs})
	}
	var e modules.Explorer
	if strings.Contains(srv.config.Siad.Modules, "e") {
		i++
		fmt.Printf("(%d/%d) Loading explorer...\n", i, len(srv.config.Siad.Modules))
		e, err = explorer.New(cs, filepath.Join(srv.config.Siad.SiaDir, modules.ExplorerDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "explorer", Closer: e})
	}
	var tpool modules.TransactionPool
	if strings.Contains(srv.config.Siad.Modules, "t") {
		i++
		fmt.Printf("(%d/%d) Loading transaction pool...\n", i, len(srv.config.Siad.Modules))
		tpool, err = transactionpool.New(cs, g, filepath.Join(srv.config.Siad.SiaDir, modules.TransactionPoolDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "transaction pool", Closer: tpool})
	}
	var w modules.Wallet
	if strings.Contains(srv.config.Siad.Modules, "w") {
		i++
		fmt.Printf("(%d/%d) Loading wallet...\n", i, len(srv.config.Siad.Modules))
		w, err = wallet.New(cs, tpool, filepath.Join(srv.config.Siad.SiaDir, modules.WalletDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "wallet", Closer: w})
	}
	var m modules.Miner
	if strings.Contains(srv.config.Siad.Modules, "m") {
		i++
		fmt.Printf("(%d/%d) Loading miner...\n", i, len(srv.config.Siad.Modules))
		m, err = miner.New(cs, tpool, w, filepath.Join(srv.config.Siad.SiaDir, modules.MinerDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "miner", Closer: m})
	}
	var h modules.Host
	if strings.Contains(srv.config.Siad.Modules, "h") {
		i++
		fmt.Printf("(%d/%d) Loading host...\n", i, len(srv.config.Siad.Modules))
		h, err = host.New(cs, g, tpool, w, srv.config.Siad.HostAddr, filepath.Join(srv.config.Siad.SiaDir, modules.HostDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "host", Closer: h})
	}
	var r modules.Renter
	if strings.Contains(srv.config.Siad.Modules, "r") {
		i++
		fmt.Printf("(%d/%d) Loading renter...\n", i, len(srv.config.Siad.Modules))
		r, err = renter.New(g, cs, w, tpool, filepath.Join(srv.config.Siad.SiaDir, modules.RenterDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "renter", Closer: r})
	}
	var p modules.Pool
	if strings.Contains(srv.config.Siad.Modules, "p") {
		i++
		fmt.Printf("(%d/%d) Loading pool...\n", i, len(srv.config.Siad.Modules))
		p, err = pool.New(cs, tpool, g, w, filepath.Join(srv.config.Siad.SiaDir, modules.PoolDir), srv.config.MiningPoolConfig)
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "pool", Closer: p})
	}
	var sm modules.StratumMiner
	if strings.Contains(srv.config.Siad.Modules, "s") {
		i++
		fmt.Printf("(%d/%d) Loading stratum miner...\n", i, len(srv.config.Siad.Modules))
		sm, err = stratumminer.New(filepath.Join(srv.config.Siad.SiaDir, modules.StratumMinerDir))
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "stratumminer", Closer: sm})
	}
	var idx modules.Index
	if strings.Contains(srv.config.Siad.Modules, "i") {
		i++
		fmt.Printf("(%d/%d) Loading index...\n", i, len(srv.config.Siad.Modules))
		idx, err = index.New(cs, tpool, g, w, filepath.Join(srv.config.Siad.SiaDir, modules.IndexDir), srv.config.IndexConfig)
		if err != nil {
			return err
		}
		srv.moduleClosers = append(srv.moduleClosers, moduleCloser{name: "idx", Closer: idx})
	}

	// Create the Sia API
	a := api.New(
		srv.config.Siad.RequiredUserAgent,
		srv.config.APIPassword,
		cs,
		e,
		g,
		h,
		m,
		r,
		tpool,
		w,
		p,
		sm,
		idx,
	)

	// connect the API to the server
	srv.mu.Lock()
	srv.api = a
	srv.mu.Unlock()

	// Attempt to auto-unlock the wallet using the SIA_WALLET_PASSWORD env variable
	if password := os.Getenv("SIAPRIME_WALLET_PASSWORD"); password != "" {
		fmt.Println("SiaPrime Wallet Password found, attempting to auto-unlock wallet")
		if err := unlockWallet(w, password); err != nil {
			fmt.Println("Auto-unlock failed.")
		} else {
			fmt.Println("Auto-unlock successful.")
		}
	}

	return nil
}

// Serve starts the HTTP server
func (srv *Server) Serve() error {
	// The server will run until an error is encountered or the listener is
	// closed, via either the Close method or the signal handling above.
	// Closing the listener will result in the benign error handled below.
	err := srv.httpServer.Serve(srv.listener)
	if err != nil && !strings.HasSuffix(err.Error(), "use of closed network connection") {
		return err
	}
	return nil
}

// Close closes the Server's listener, causing the HTTP server to shut down.
func (srv *Server) Close() error {
	var errs []error
	// Close the listener, which will cause Server.Serve() to return.
	if err := srv.listener.Close(); err != nil {
		errs = append(errs, err)
	}
	// Close all of the modules in reverse order
	for i := len(srv.moduleClosers) - 1; i >= 0; i-- {
		m := srv.moduleClosers[i]
		fmt.Printf("Closing %v...\n", m.name)
		if err := m.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return build.JoinErrors(errs, "\n")
}
