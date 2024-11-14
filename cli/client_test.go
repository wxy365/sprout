package cli

import (
	"context"
	sp "github.com/wxy365/sprout"
	"net/http"
	"testing"
	"time"
)

func TestDoer(t *testing.T) {
	cli := NewH3Client(time.Second)
	doer := Doer[DemoIn, DemoOut](cli, http.MethodPost, "https://localhost:443/demo/{id}/{name}", sp.MimeJson)
	in := &DemoIn{
		Name:    "wxy",
		Id:      1209,
		Hobbies: []string{"pingpong", "basketball"},
	}
	out, err := doer(context.Background(), in)
	if err != nil {
		t.Error(err)
	} else {
		t.Logf("%+v", out)
	}
}

type DemoIn struct {
	Name    string   `path:"name"`
	Id      int64    `path:"id"`
	Hobbies []string `json:"hobbies"`
}

type DemoOut struct {
	Message string `json:"message"`
}

type DemoAppCtx struct {
	Ins []DemoIn `env:"DEMO_APP_INS" default:"[{\"id\":123, \"name\":\"xixi\"}]"`
}
