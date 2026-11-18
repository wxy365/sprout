package sprout

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"

	"github.com/wxy365/basal/cfg"
	"github.com/wxy365/basal/cfg/def"
	"github.com/wxy365/basal/rflt"
)

// app is a generic application container that bootstraps and manages the lifecycle
// of HTTP servers, endpoints, interceptors, and shared application context.
//
// The exported alias App is declared in api.go, which collects the public API
// surface of the package.
type app[C any] struct {
	// Name is the application name, used for identification and server naming.
	Name string
	// DecryptFns is a map of decryption algorithm names to their corresponding
	// decryption functions, used for decrypting request field values.
	DecryptFns map[string]func(cipher []byte) ([]byte, error)
	// EncryptFns is a map of encryption algorithm names to their corresponding
	// encryption functions.
	EncryptFns map[string]func(plain []byte) ([]byte, error)
	// Initializers is a list of initialization functions called during application startup.
	Initializers []appInitializer[C]
	// Servers is the list of HTTP servers managed by this application.
	Servers []*httpServer
	// Runners is the list of the components that are started together with this
	// application, such as the gRPC servers. Their Start method blocks until
	// the component stops, so all of them but the first one run in the
	// background.
	Runners []runner
	// Context is the shared application context, typically a pointer to a struct
	// that holds application-wide dependencies such as configs and service references.
	Context C

	once sync.Once
}

// runner is a component that is started and stopped together with the
// application.
//
// The exported alias Runner is declared in api.go, which collects the public
// API surface of the package.
type runner interface {
	// Start blocks until the component stops.
	Start() error
	// Stop shuts the component down gracefully.
	Stop(ctx context.Context) error
}

func (a *app[C]) Run() {
	a.once.Do(func() {
		fmt.Print(`
   _____                        __
  / ___/____  _________  __  __/ /_
  \__ \/ __ \/ ___/ __ \/ / / / __/
 ___/ / /_/ / /  / /_/ / /_/ / /_
/____/ .___/_/   \____/\__,_/\__/
    /_/
`)
		a.init()
		starts := make([]func() error, 0, len(a.Servers)+len(a.Runners))
		for _, server := range a.Servers {
			starts = append(starts, func() error {
				server.start()
				return nil
			})
		}
		for _, r := range a.Runners {
			starts = append(starts, r.Start)
		}
		for i := len(starts); i > 0; i-- {
			start := starts[i-1]
			if i > 1 {
				go func() {
					if err := start(); err != nil {
						log.ErrorErr(err)
					}
				}()
			} else {
				if err := start(); err != nil {
					log.ErrorErr(err)
				}
			}
		}
	})
}

func (a *app[C]) init() {
	totalStart := time.Now()
	var err error

	nameStart := time.Now()
	a.Name, err = def.GetStr("app.name", a.Name)
	if err != nil && !cfg.IsCfgMissingErr(err) {
		panic(err)
	}
	if a.Name == "" {
		processPath := os.Args[0]
		a.Name = filepath.Base(processPath)
	}
	nameDuration := time.Since(nameStart)

	contextStart := time.Now()
	t := reflect.TypeOf(a.Context)
	if t.Kind() != reflect.Pointer {
		panic("The app Context should be pointer kind")
	}

	if reflect.ValueOf(a.Context).IsNil() {
		appCtx := reflect.New(t.Elem()).Interface()
		a.Context = appCtx.(C)
	}
	initAppContextAttribute(a)
	contextDuration := time.Since(contextStart)

	serversStart := time.Now()
	svrCfgs, err := def.GetObj[[]svrCfg]("app.servers")
	if err != nil && !cfg.IsCfgMissingErr(err) {
		panic(err)
	}
	for _, scfg := range svrCfgs {
		if scfg.Name == "" {
			log.Panic("Server name must not be empty! Please check the configuration files.")
		}
		var svrCreated bool
		for _, svr := range a.Servers {
			if svr.Name == scfg.Name { // the configruation has higher priority
				svrCreated = true
				if scfg.Port > 0 {
					svr.Port = scfg.Port
				}
				if scfg.CertFile != "" {
					svr.CertFile = scfg.CertFile
				}
				if scfg.KeyFile != "" {
					svr.KeyFile = scfg.KeyFile
				}
				if scfg.Debug != nil {
					svr.Debug = *scfg.Debug
				}
				if scfg.ShutdownTimeout > 0 {
					svr.ShutdownTimeout = time.Millisecond * time.Duration(scfg.ShutdownTimeout)
				}
				break
			}
		}
		if !svrCreated {
			svr := newDefaultServer(scfg.Name)
			svr.Port = scfg.Port
			svr.CertFile = scfg.CertFile
			svr.KeyFile = scfg.KeyFile
			if scfg.ShutdownTimeout > 0 {
				svr.ShutdownTimeout = time.Millisecond * time.Duration(scfg.ShutdownTimeout)
			}
			if scfg.Debug != nil {
				svr.Debug = *scfg.Debug
			}
			a.Servers = append(a.Servers, svr)
		}
	}

	svrNames := make(map[string]struct{})
	for _, svr := range a.Servers {
		if _, ok := svrNames[svr.Name]; ok {
			log.Panic("Duplicate server name: [{0}]", svr.Name)
		}
		svrNames[svr.Name] = struct{}{}

		if len(svr.Validators) == 0 {
			svr.Validators = defaultValidators()
		}
		if svr.ErrorHandler == nil {
			svr.ErrorHandler = defaultErrHandler
		}
	}
	serversDuration := time.Since(serversStart)

	initializersStart := time.Now()
	for _, initializer := range a.Initializers {
		err = initializer(a)
		if err != nil {
			panic(err)
		}
	}
	initializersDuration := time.Since(initializersStart)

	totalDuration := time.Since(totalStart)
	configDuration := def.LoadDuration()

	log.Debug("Startup stage timing: name={0}", formatDuration(nameDuration))
	log.Debug("Startup stage timing: context={0}", formatDuration(contextDuration))
	log.Debug("Startup stage timing: servers={0}", formatDuration(serversDuration))
	log.Debug("Startup stage timing: initializers={0}", formatDuration(initializersDuration))
	log.Debug("Startup stage timing: config={0}", formatDuration(configDuration))
	log.Info("Startup timing: name={0} context={1} servers={2} initializers={3} total={4} config={5}",
		formatDuration(nameDuration),
		formatDuration(contextDuration),
		formatDuration(serversDuration),
		formatDuration(initializersDuration),
		formatDuration(totalDuration),
		formatDuration(configDuration),
	)
}

// formatDuration renders a duration with a tenth of a millisecond precision,
// e.g. 1.2ms.
func formatDuration(d time.Duration) string {
	return d.Round(100 * time.Microsecond).String()
}

// appInitializer is a function type for application initialization callbacks.
// It is called during App.Run() after all configuration has been loaded.
// If an initializer returns an error, the application panics.
//
// The exported alias AppInitializer is declared in api.go, which collects the
// public API surface of the package.
type appInitializer[C any] func(a *App[C]) error

// mount registers an endpoint onto a server within the application.
// The endpoint is wired up via an AppInitializer, ensuring it is registered before
// the server starts. If no server name is provided, the endpoint is mounted on
// the first server in the App.Servers slice.
//
// The exported wrapper Mount is declared in api.go, which collects the public
// API surface of the package.
func mount[C any, I any, O any](e *Endpoint[I, O], a *App[C], svrName ...string) {
	mountEndpoint(a, svrName, e.Name, e.appendToServer)
}

// mountSSE registers an SSE endpoint onto a server within the application.
//
// The exported wrapper MountSSE is declared in api.go, which collects the
// public API surface of the package.
func mountSSE[C any, I any](e *SSEEndpoint[I], a *App[C], svrName ...string) {
	mountEndpoint(a, svrName, e.Name, e.appendToServer)
}

// mountEndpoint wires an endpoint onto a server within the application. The
// endpoint is wired up via an AppInitializer, ensuring it is registered before
// the server starts. If no server name is provided, the endpoint is mounted on
// the first server in the App.Servers slice.
//
// The appendToServer is the registration logic of the endpoint itself, which
// the ordinary endpoints and the SSE endpoints provide on their own.
func mountEndpoint[C any](a *App[C], svrName []string, endpointName string,
	appendToServer func(svr *httpServer, decrypters map[string]func(cipher []byte) ([]byte, error))) {
	a.Initializers = append(a.Initializers, func(app *App[C]) error {
		if len(svrName) == 0 {
			if len(app.Servers) == 0 {
				app.Servers = append(app.Servers, newDefaultServer(app.Name+"_server"))
			}
			appendToServer(app.Servers[0], app.DecryptFns)
		} else {
			var mounted bool
			for _, svr := range app.Servers {
				if svr.Name == svrName[0] {
					appendToServer(svr, app.DecryptFns)
					mounted = true
					break
				}
			}
			if !mounted {
				log.Panic("Failed to mount endpoint [{0}] to server [{1}]: server not defined", endpointName, svrName[0])
			}
		}
		return nil
	})
}

func initAppContextAttribute[C any](a *App[C]) {
	t := reflect.TypeOf(a.Context)
	v := reflect.ValueOf(a.Context)
	for v.Kind() == reflect.Pointer {
		t = t.Elem()
		v = v.Elem()
	}
	populateObject(v, t, "")
}

func populateObject(val reflect.Value, typ reflect.Type, keyPrefix string) {
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		fv := val.Field(i)
		ft := f.Type
		// The "map" tag is the canonical name of the configuration tag. The
		// "cfg" tag is kept as a deprecated alias of it.
		cfgKey := f.Tag.Get("map")
		if cfgKey == "" {
			cfgKey = f.Tag.Get("cfg")
		}

		for fv.Kind() == reflect.Pointer {
			fv = fv.Elem()
			ft = ft.Elem()
		}

		if defStrValue, ok := f.Tag.Lookup("default"); ok && fv.IsValid() {
			err := rflt.UnmarshalValue(fv, defStrValue)
			if err != nil {
				panic(err)
			}
		}
		if cfgKey == "" {
			continue
		}
		if ft.Kind() == reflect.Struct {
			populateObject(fv, ft, keyPrefix+cfgKey+".")
		} else {
			envValue, _ := def.GetStr(keyPrefix + cfgKey)
			if len(envValue) > 0 {
				err := rflt.UnmarshalValue(fv, envValue)
				if err != nil {
					panic(errs.Wrap(err, "Cannot resolve configuration item [{0}]", cfgKey))
				}
			}
		}
	}
}
