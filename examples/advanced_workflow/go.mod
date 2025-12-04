module github.com/ehabterra/workflow/examples/advanced_workflow

go 1.23

require (
	github.com/ehabterra/workflow v0.0.0
	github.com/go-chi/chi/v5 v5.2.1
	github.com/gorilla/sessions v1.4.0
	github.com/mattn/go-sqlite3 v1.14.32
)

require (
	github.com/expr-lang/expr v1.17.6 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/ehabterra/workflow => ../../
