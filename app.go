package svr

import (
	"gitee.com/wxy25/common/env"
	"gitee.com/wxy25/common/rflt"
	"reflect"
)

type App[C any] struct {
	name      string
	servers   []*Server
	context   C
	decryptFn func(str string) (string, error)
}

func (a *App[C]) Run() {
	for i := len(a.servers); i > 0; i-- {
		server := a.servers[i-1]
		if i > 1 {
			go func() {
				server.Start()
			}()
		} else {
			server.Start()
		}
	}
}

func (a *App[C]) Name() string {
	return a.name
}

func (a *App[C]) Context() C {
	return a.context
}

// todo 获取数据库
func (a *App[C]) DB(dbName string) any {
	return nil
}

// todo 获取分布式缓存组件
func (a *App[C]) Cache(cacheName string) any {
	return nil
}

// todo 获取客户端，特指http客户端
func (a *App[C]) Client(dbName string) any {
	return nil
}

type appInitializer[C any] func(app *App[C]) error

type AppBuilder[C any] struct {
	app            *App[C]
	done           bool
	name           string
	decryptFn      func(str string) (string, error)
	initializers   []appInitializer[C]
	servers        []*Server
	serverBuilders []*ServerBuilder
}

func NewAppBuilder[C any]() *AppBuilder[C] {
	return &AppBuilder[C]{}
}

func (a *AppBuilder[C]) Name(name string) *AppBuilder[C] {
	a.name = name
	return a
}

func (a *AppBuilder[C]) DecryptFn(fn func(str string) (string, error)) *AppBuilder[C] {
	a.decryptFn = fn
	return a
}

func (a *AppBuilder[C]) AddInitializer(fn ...appInitializer[C]) *AppBuilder[C] {
	a.initializers = append(a.initializers, fn...)
	return a
}

func (a *AppBuilder[C]) AddServer(s ...*Server) *AppBuilder[C] {
	a.servers = append(a.servers, s...)
	return a
}

func (a *AppBuilder[C]) AddServerBuilder(sb ...*ServerBuilder) *AppBuilder[C] {
	a.serverBuilders = append(a.serverBuilders, sb...)
	return a
}

func (a *AppBuilder[C]) Build() *App[C] {
	if a.done {
		return a.app
	}
	app := App[C]{
		name:      a.name,
		decryptFn: a.decryptFn,
		servers:   a.servers,
	}
	var c C
	t := reflect.TypeOf(c)
	if t.Kind() != reflect.Pointer {
		panic("The app context is supposed to be of pointer kind, please add operator '*' to the AppBuilder type parameter, e.g. AppBuilder[*C]")
	}
	appCtx := reflect.New(t.Elem()).Interface()
	app.context = appCtx.(C)
	initAppContextAttribute[C](&app)
	for _, initializer := range a.initializers {
		err := initializer(&app)
		if err != nil {
			panic(err)
		}
	}
	if reflect.ValueOf(appCtx).Elem().UnsafeAddr() != reflect.ValueOf(app.context).Elem().UnsafeAddr() {
		// if user replaced the context, then initialize the attributes again
		initAppContextAttribute[C](&app)
	}
	for _, sb := range a.serverBuilders {
		a.servers = append(a.servers, sb.Build())
	}
	app.servers = a.servers
	a.app = &app
	a.done = true
	return &app
}

func initAppContextAttribute[C any](a *App[C]) {
	t := reflect.TypeOf(a.context).Elem()
	v := reflect.ValueOf(a.context).Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if defStrValue, ok := f.Tag.Lookup("default"); ok {
			err := rflt.PopulateValueFromString(fv, defStrValue)
			if err != nil {
				panic(err)
			}
		}
		if envKey, ok := f.Tag.Lookup("env"); ok {
			if len(envKey) > 0 {
				envValue, _ := env.GetStr(envKey)
				if len(envValue) > 0 {
					err := rflt.PopulateValueFromString(fv, envValue)
					if err != nil {
						panic(err)
					}
				}
			}
		}
	}
}
