package sprout

import (
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

// App is a generic application container that bootstraps and manages the lifecycle
// of HTTP servers, endpoints, interceptors, and shared application context.
type App[C any] struct {
	// Name is the application name, used for identification and server naming.
	Name string
	// DecryptFns is a map of decryption algorithm names to their corresponding
	// decryption functions, used for decrypting request field values.
	DecryptFns map[string]func(cipher []byte) ([]byte, error)
	// EncryptFns is a map of encryption algorithm names to their corresponding
	// encryption functions.
	EncryptFns map[string]func(plain []byte) ([]byte, error)
	// Initializers is a list of initialization functions called during application startup.
	Initializers []AppInitializer[C]
	// Servers is the list of HTTP servers managed by this application.
	Servers []*Server
	// Context is the shared application context, typically a pointer to a struct
	// that holds application-wide dependencies such as configs and service references.
	Context C

	once sync.Once
}

func (a *App[C]) Run() {
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
		for i := len(a.Servers); i > 0; i-- {
			server := a.Servers[i-1]
			if i > 1 {
				go func() {
					server.start()
				}()
			} else {
				server.start()
			}
		}
	})
}

func (a *App[C]) init() {
	var err error
	a.Name, err = def.GetStr("app.Name", a.Name)
	if err != nil && !cfg.IsCfgMissingErr(err) {
		panic(err)
	}
	if a.Name == "" {
		processPath := os.Args[0]
		a.Name = filepath.Base(processPath)
	}

	t := reflect.TypeOf(a.Context)
	if t.Kind() != reflect.Pointer {
		panic("The app Context should be pointer kind")
	}

	if reflect.ValueOf(a.Context).IsNil() {
		appCtx := reflect.New(t.Elem()).Interface()
		a.Context = appCtx.(C)
	}
	initAppContextAttribute(a)

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

	for _, initializer := range a.Initializers {
		err = initializer(a)
		if err != nil {
			panic(err)
		}
	}
}

// AppInitializer is a function type for application initialization callbacks.
// It is called during App.Run() after all configuration has been loaded.
// If an initializer returns an error, the application panics.
type AppInitializer[C any] func(app *App[C]) error

// Mount registers an endpoint onto a server within the application.
// The endpoint is wired up via an AppInitializer, ensuring it is registered before
// the server starts. If no server name is provided, the endpoint is mounted on
// the first server in the App.Servers slice.
func Mount[C any, I any, O any](e *Endpoint[I, O], a *App[C], svrName ...string) {
	a.Initializers = append(a.Initializers, func(app *App[C]) error {
		if len(svrName) == 0 {
			if len(app.Servers) == 0 {
				app.Servers = append(app.Servers, newDefaultServer(app.Name+"_server"))
			}
			e.appendToServer(app.Servers[0], app.DecryptFns)
		} else {
			var mounted bool
			for _, svr := range app.Servers {
				if svr.Name == svrName[0] {
					e.appendToServer(svr, app.DecryptFns)
					mounted = true
					break
				}
			}
			if !mounted {
				log.Panic("Failed to mount endpoint [{0}] to server [{1}]: server not defined", e.Name, svrName[0])
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
		cfgKey := f.Tag.Get("cfg")

		for fv.Kind() == reflect.Pointer {
			fv = fv.Elem()
			ft = ft.Elem()
		}

		if defStrValue, ok := f.Tag.Lookup("default"); ok {
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
