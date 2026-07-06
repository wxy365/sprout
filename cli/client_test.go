package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sp "github.com/wxy365/sprout"
)

func TestDoer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", sp.MimeJson)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "ok"})
	}))
	defer srv.Close()

	cli := NewClient(time.Second)
	url := fmt.Sprintf("%s/demo/{id}/{name}", srv.URL)
	doer := Doer[DemoIn, DemoOut](cli, http.MethodPost, url, sp.MimeJson)
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
