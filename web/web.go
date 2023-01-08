package web

import (
	_ "embed"
	"html/template"
	"log"
	"net/http"
)

//go:embed index.html
var index []byte

func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		indexTemplate := template.Must(template.New("").Parse(string(index)))
		if err := indexTemplate.Execute(w, "ws://"+r.Host+"/websocket"); err != nil {
			log.Fatal(err)
		}
	}
}
