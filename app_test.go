package sprout

import (
	"net/http"
	"testing"
)

// appContext is the application context used by the app tests. The "cfg" tags
// are populated when the application is initialized.
type appContext struct {
	ServerName string `cfg:"server-name"`
	LegacyName string `cfg:"legacy-name"`
	MaxSize    int    `cfg:"max-size" default:"300"`
	Enabled    bool   `default:"true"`
}

// appTestConfig is a minimal configuration that initializes the default
// configuration of the framework. The application refuses to start without
// one. The name of the application is declared under both spellings, so that
// the test does not depend on the spelling that the framework looks up.
const appTestConfig = `{"app":{"Name":"test-app","name":"test-app"},"server-name":"from-config","legacy-name":"from-legacy","max-size":"456"}`

// TestInitAppContextAttribute verifies that the configuration values and the
// default values are written into the application context.
func TestInitAppContextAttribute(t *testing.T) {
	restoreDefaultConfig(t, appTestConfig)

	ctx := &appContext{}
	app := &App[*appContext]{Context: ctx}
	initAppContextAttribute(app)

	if ctx.ServerName != "from-config" {
		t.Errorf("the ServerName = %q, want %q", ctx.ServerName, "from-config")
	}
	if ctx.LegacyName != "from-legacy" {
		t.Errorf("the LegacyName = %q, want %q", ctx.LegacyName, "from-legacy")
	}
	if ctx.MaxSize != 456 {
		t.Errorf("the MaxSize = %d, want %d", ctx.MaxSize, 456)
	}
	if !ctx.Enabled {
		t.Error("the Enabled flag should be true")
	}
}

// TestAppInitRequiresPointerContext verifies that the application context must
// be a pointer.
func TestAppInitRequiresPointerContext(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("a non-pointer application context should be rejected")
		}
	}()
	app := &App[appContext]{Context: appContext{}}
	app.init()
}

// TestAppInitCreatesContext verifies that the application creates the context
// when it is nil.
func TestAppInitCreatesContext(t *testing.T) {
	restoreDefaultConfig(t, appTestConfig)

	app := &App[*appContext]{Context: nil}
	app.init()

	if app.Context == nil {
		t.Fatal("the application context should have been created")
	}
	if app.Context.MaxSize != 456 {
		t.Errorf("the MaxSize = %d, want %d", app.Context.MaxSize, 456)
	}
}

// TestAppInitReadsTheName verifies that the application reads its name from
// the configuration.
func TestAppInitReadsTheName(t *testing.T) {
	restoreDefaultConfig(t, appTestConfig)

	app := &App[*appContext]{Context: &appContext{}}
	app.init()

	if app.Name != "test-app" {
		t.Errorf("the application name = %q, want %q", app.Name, "test-app")
	}
}

// TestMountRegistersEndpoint verifies that a mounted endpoint is registered on
// the server of the application.
func TestMountRegistersEndpoint(t *testing.T) {
	restoreDefaultConfig(t, appTestConfig)

	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello"}, nil
		},
	}

	app := &App[*appContext]{Context: &appContext{}}
	Mount(e, app)

	if len(app.Initializers) != 1 {
		t.Fatalf("the number of initializers = %d, want %d", len(app.Initializers), 1)
	}
	if err := app.Initializers[0](app); err != nil {
		t.Fatalf("the initializer should not fail, but got: %v", err)
	}
	if len(app.Servers) != 1 {
		t.Fatalf("the number of servers = %d, want %d", len(app.Servers), 1)
	}
	if len(app.Servers[0].endpoints) != 1 {
		t.Errorf("the number of endpoints = %d, want %d", len(app.Servers[0].endpoints), 1)
	}
}

// TestMountOnNamedServer verifies that an endpoint can be mounted on a named
// server.
func TestMountOnNamedServer(t *testing.T) {
	restoreDefaultConfig(t, appTestConfig)

	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello"}, nil
		},
	}

	app := &App[*appContext]{
		Context: &appContext{},
		Servers: []*Server{
			newDefaultServer("first"),
			newDefaultServer("second"),
		},
	}
	Mount(e, app, "second")

	if err := app.Initializers[0](app); err != nil {
		t.Fatalf("the initializer should not fail, but got: %v", err)
	}
	if len(app.Servers[0].endpoints) != 0 {
		t.Errorf("the first server should not have any endpoint")
	}
	if len(app.Servers[1].endpoints) != 1 {
		t.Errorf("the second server should have one endpoint, but got %d", len(app.Servers[1].endpoints))
	}
}

// TestMountOnUnknownServer verifies that mounting an endpoint on a server that
// does not exist is rejected.
func TestMountOnUnknownServer(t *testing.T) {
	restoreDefaultConfig(t, appTestConfig)

	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return &endpointOutput{Greeting: "hello"}, nil
		},
	}

	app := &App[*appContext]{
		Context: &appContext{},
		Servers: []*Server{newDefaultServer("first")},
	}
	Mount(e, app, "missing")

	defer func() {
		if r := recover(); r == nil {
			t.Error("mounting on an unknown server should be rejected")
		}
	}()
	_ = app.Initializers[0](app)
}

// TestAppInitDuplicateServerName verifies that two servers cannot share the
// same name.
func TestAppInitDuplicateServerName(t *testing.T) {
	restoreDefaultConfig(t, appTestConfig)

	app := &App[*appContext]{
		Context: &appContext{},
		Servers: []*Server{
			newDefaultServer("same"),
			newDefaultServer("same"),
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("the duplicate server name should be rejected")
		}
	}()
	app.init()
}

// TestAppInitFromConfig verifies that the servers described by the
// configuration are created.
func TestAppInitFromConfig(t *testing.T) {
	restoreDefaultConfig(t, `{"app":{"name":"test-app","servers":[
		{"name":"pub","port":8080,"debug":true,"shutdown_timeout":5000}
	]}}`)

	app := &App[*appContext]{Context: &appContext{}}
	app.init()

	if len(app.Servers) != 1 {
		t.Fatalf("the number of servers = %d, want %d", len(app.Servers), 1)
	}
	svr := app.Servers[0]
	if svr.Name != "pub" {
		t.Errorf("the server name = %q, want %q", svr.Name, "pub")
	}
	if svr.Port != 8080 {
		t.Errorf("the server port = %d, want %d", svr.Port, 8080)
	}
	if !svr.Debug {
		t.Error("the debug flag should be enabled")
	}
	if svr.ShutdownTimeout.Milliseconds() != 5000 {
		t.Errorf("the shutdown timeout = %d ms, want %d", svr.ShutdownTimeout.Milliseconds(), 5000)
	}
}
