package svr

type Named interface {
	Name() string
}

type Ordered interface {
	Order() int
}
